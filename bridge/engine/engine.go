// Package engine 实现 Croc 引擎的任务管理器。它把每次收发隔离为独立
// Context/Client/事件队列，并通过 main 包的 CGO 导出形成稳定的 C ABI。
package engine

import (
	"errors"
	"io"
	"sync"
	"time"

	"github.com/schollz/croc/v11/src/croc"
	"github.com/schollz/croc/v11/src/logger"
)

const (
	// APIVersion 是公开 C ABI 的版本。
	APIVersion = "1.0.0"
	// EngineVersion 是与本库链接的 Croc 核心版本。
	EngineVersion = "11.6.0"
	// ShutdownTimeout 是关闭引擎时等待活动任务安全退出的上限。
	ShutdownTimeout = 20 * time.Second
)

// Engine 是进程级引擎实例。所有公开操作均可从任意线程调用。
type Engine struct {
	mu           sync.Mutex
	initialized  bool
	shuttingDown bool
	nextTaskID   uint64
	tasks        map[uint64]*Task
	queue        *eventQueue
}

func New() *Engine {
	return &Engine{
		tasks: make(map[uint64]*Task),
		queue: newEventQueue(DefaultEventQueueCapacity),
	}
}

// Initialize 具有幂等语义：已初始化时返回 CodeAlreadyInit。
func (e *Engine) Initialize() int32 {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.initialized {
		return CodeAlreadyInit
	}
	// 库模式下日志不得污染宿主进程的 stdout，也不把传输细节写入宿主日志。
	logger.SetOutput(io.Discard)
	croc.Debug(false)
	e.initialized = true
	e.shuttingDown = false
	SetLastError("")
	return CodeOK
}

func (e *Engine) Initialized() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.initialized
}

// Shutdown 取消所有活动任务并等待它们退出。不会调用 os.Exit()。
func (e *Engine) Shutdown() (int32, error) {
	e.mu.Lock()
	if !e.initialized {
		e.mu.Unlock()
		return CodeOK, nil
	}
	if e.shuttingDown {
		e.mu.Unlock()
		return CodeShuttingDown, errShuttingDown
	}
	e.shuttingDown = true
	tasks := make([]*Task, 0, len(e.tasks))
	for _, task := range e.tasks {
		tasks = append(tasks, task)
	}
	e.mu.Unlock()

	for _, task := range tasks {
		task.cancelTask()
	}
	deadline := time.Now().Add(ShutdownTimeout)
	for _, task := range tasks {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			e.mu.Lock()
			e.shuttingDown = false
			e.mu.Unlock()
			return CodeTimeout, errors.New("timed out waiting for active tasks to stop")
		}
		timer := time.NewTimer(remaining)
		select {
		case <-task.finished:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
			e.mu.Lock()
			e.shuttingDown = false
			e.mu.Unlock()
			return CodeTimeout, errors.New("timed out waiting for active tasks to stop")
		}
	}

	e.mu.Lock()
	e.tasks = make(map[uint64]*Task)
	e.queue.reset()
	e.initialized = false
	e.shuttingDown = false
	e.mu.Unlock()
	return CodeOK, nil
}

func (e *Engine) beginTask(direction string, promptTimeout time.Duration) (*Task, int32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.initialized {
		return nil, CodeNotInitialized, errNotInitialized
	}
	if e.shuttingDown {
		return nil, CodeShuttingDown, errShuttingDown
	}
	e.nextTaskID++
	task := newTask(e, e.nextTaskID, direction, promptTimeout)
	e.tasks[task.id] = task
	return task, CodeOK, nil
}

// StartSend 异步启动发送任务。配置/路径错误在启动前同步返回。
func (e *Engine) StartSend(data []byte) (uint64, int32, error) {
	cfg, options, err := parseSendConfig(data)
	if err != nil {
		return 0, CodeInvalidConfig, err
	}
	task, code, err := e.beginTask("send", time.Duration(cfg.PromptTimeoutSec)*time.Second)
	if err != nil {
		return 0, code, err
	}
	go task.execute(&cfg, nil, options)
	return task.id, CodeOK, nil
}

// StartReceive 异步启动接收任务。
func (e *Engine) StartReceive(data []byte) (uint64, int32, error) {
	cfg, options, err := parseReceiveConfig(data)
	if err != nil {
		return 0, CodeInvalidConfig, err
	}
	task, code, err := e.beginTask("receive", time.Duration(cfg.PromptTimeoutSec)*time.Second)
	if err != nil {
		return 0, code, err
	}
	task.savePath = cfg.SavePath
	go task.execute(nil, &cfg, options)
	return task.id, CodeOK, nil
}

func (e *Engine) task(taskID uint64) (*Task, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.initialized {
		return nil, errNotInitialized
	}
	task := e.tasks[taskID]
	if task == nil {
		return nil, errTaskNotFound
	}
	return task, nil
}

func (e *Engine) CancelTask(taskID uint64) (int32, error) {
	task, err := e.task(taskID)
	if err != nil {
		if errors.Is(err, errNotInitialized) {
			return CodeNotInitialized, err
		}
		return CodeTaskNotFound, err
	}
	task.cancelTask()
	return CodeOK, nil
}

func (e *Engine) IsTaskFinished(taskID uint64) (bool, int32, error) {
	task, err := e.task(taskID)
	if err != nil {
		if errors.Is(err, errNotInitialized) {
			return false, CodeNotInitialized, err
		}
		return false, CodeTaskNotFound, err
	}
	select {
	case <-task.finished:
		return true, CodeOK, nil
	default:
		return false, CodeOK, nil
	}
}

func (e *Engine) TaskSnapshot(taskID uint64) ([]byte, int32, error) {
	task, err := e.task(taskID)
	if err != nil {
		if errors.Is(err, errNotInitialized) {
			return nil, CodeNotInitialized, err
		}
		return nil, CodeTaskNotFound, err
	}
	data, marshalErr := task.snapshotJSON()
	if marshalErr != nil {
		return nil, CodeInternal, marshalErr
	}
	return data, CodeOK, nil
}

func (e *Engine) RespondToPrompt(taskID uint64, promptID string, decision int32) (int32, error) {
	task, err := e.task(taskID)
	if err != nil {
		if errors.Is(err, errNotInitialized) {
			return CodeNotInitialized, err
		}
		return CodeTaskNotFound, err
	}
	if err := task.RespondPrompt(promptID, decision); err != nil {
		if errors.Is(err, errPromptNotFound) {
			return CodePromptNotFound, err
		}
		return CodeInvalidDecision, err
	}
	return CodeOK, nil
}

func (e *Engine) ReleaseTask(taskID uint64) (int32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.initialized {
		return CodeNotInitialized, errNotInitialized
	}
	task := e.tasks[taskID]
	if task == nil {
		return CodeTaskNotFound, errTaskNotFound
	}
	select {
	case <-task.finished:
		delete(e.tasks, taskID)
		return CodeOK, nil
	default:
		return CodeTaskBusy, errTaskBusy
	}
}

func (e *Engine) PollEvent(write func([]byte) bool) (int32, int, error) {
	e.mu.Lock()
	initialized := e.initialized
	e.mu.Unlock()
	if !initialized {
		return CodeNotInitialized, 0, errNotInitialized
	}
	code, required := e.queue.poll(write)
	if code == CodeNoEvent || code == CodeEventAvailable {
		return code, required, nil
	}
	return code, required, nil
}

func (e *Engine) TaskCount() int32 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return int32(len(e.tasks))
}

func (e *Engine) ActiveTaskCount() int32 {
	e.mu.Lock()
	tasks := make([]*Task, 0, len(e.tasks))
	for _, task := range e.tasks {
		tasks = append(tasks, task)
	}
	e.mu.Unlock()
	var active int32
	for _, task := range tasks {
		select {
		case <-task.finished:
		default:
			active++
		}
	}
	return active
}

func (e *Engine) DroppedEventCount() uint64 {
	return e.queue.droppedCount()
}
