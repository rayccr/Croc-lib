package engine

import (
	"bytes"
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/minio/highwayhash"
	"github.com/schollz/croc/v11/src/croc"
	"github.com/schollz/croc/v11/src/utils"
)

const (
	taskRunning int32 = iota
	taskCompleted
	taskCancelled
	taskFailed
)

var (
	errTaskCanceledByUser = errors.New("task canceled by caller")
	highwayKey            = []byte{
		0x15, 0x53, 0xc5, 0x38, 0x3f, 0xb0, 0xb8, 0x65,
		0x78, 0xc3, 0x31, 0x0d, 0xa6, 0x65, 0xb4, 0xf6,
		0xe0, 0x52, 0x1a, 0xcf, 0x22, 0xeb, 0x58, 0xa9,
		0x95, 0x32, 0xff, 0xed, 0x02, 0xa6, 0xb1, 0x15,
	}
)

// FileState 是单个文件的真实进度。网络路径只更新原子计数，不持有任务锁。
type FileState struct {
	Index           int
	FolderRemote    string
	Name            string
	Path            string
	CreatedIdentity string
	Size            int64
	Done            atomic.Int64
	Session         atomic.Int64
	Reused          atomic.Int64
	Skipped         atomic.Bool
	skippedReason   atomic.Pointer[string]
	Failed          atomic.Bool
	Unavailable     atomic.Bool
	Verified        atomic.Bool
	Verifying       atomic.Bool
}

type pendingPrompt struct {
	request  croc.PromptRequest
	response chan bool
}

// Task 对应一个独立的 Croc Client、Context、取消控制、进度和提示注册表。
// 公开 C ABI 只使用 uint64 任务 ID，不暴露该对象指针。
type Task struct {
	id        uint64
	direction string
	engine    *Engine
	queue     *eventQueue

	ctx    context.Context
	cancel context.CancelCauseFunc

	startedAt time.Time

	state     atomic.Int32
	finalized atomic.Bool

	// eventMu 保证单个任务的事件顺序：所有阶段/清单事件先于终态事件入队，
	// 终态之后不再出现该任务的迟到阶段事件。
	eventMu sync.Mutex

	phaseMu sync.Mutex
	phase   string

	resultMu   sync.Mutex
	errorText  string
	errorCode  string
	finishedAt time.Time

	localRelayPort atomic.Pointer[string]
	peerAddress    atomic.Pointer[string]
	client         *croc.Client

	filesMu       sync.RWMutex
	files         []*FileState
	totalFolders  int
	savePath      string
	hashAlgorithm string

	totalBytes   atomic.Int64
	totalFiles   atomic.Int32
	totalDone    atomic.Int64
	sessionBytes atomic.Int64
	currentIndex atomic.Int64
	skippedFiles atomic.Int32

	verifyMu          sync.Mutex
	verifyStatus      string
	verifyBytesKnown  bool
	verifySpeedBps    float64
	verifyDone        atomic.Int64
	verifyTotal       atomic.Int64
	verifyFilesTotal  atomic.Int32
	verifyFilesDone   atomic.Int32
	verifyFilesFailed atomic.Int32

	speedMu          sync.Mutex
	speedBps         float64
	etaSeconds       *float64
	lastSampleTime   time.Time
	lastSampleBytes  int64
	lastVerifySample int64
	lastBytesChange  time.Time
	lastVerifyChange time.Time

	promptMu      sync.Mutex
	prompts       map[string]*pendingPrompt
	promptSeq     atomic.Uint64
	promptTimeout time.Duration

	reporterStop chan struct{}
	reporterDone chan struct{}
	finished     chan struct{}
	finishOnce   sync.Once

	bundleUsed bool

	lastEmitSession int64
	lastEmitVerify  int64
	lastEmitPhase   string
	lastEmitTime    time.Time
}

func newTask(engine *Engine, id uint64, direction string, promptTimeout time.Duration) *Task {
	ctx, cancel := context.WithCancelCause(context.Background())
	task := &Task{
		id:            id,
		direction:     direction,
		engine:        engine,
		queue:         engine.queue,
		ctx:           ctx,
		cancel:        cancel,
		startedAt:     time.Now(),
		phase:         "preparing",
		verifyStatus:  "not_started",
		promptTimeout: promptTimeout,
		prompts:       make(map[string]*pendingPrompt),
		reporterStop:  make(chan struct{}),
		reporterDone:  make(chan struct{}),
		finished:      make(chan struct{}),
	}
	if direction == "send" {
		task.verifyStatus = "not_applicable"
	}
	task.currentIndex.Store(-1)
	task.lastSampleTime = task.startedAt
	task.lastBytesChange = task.startedAt
	task.lastVerifyChange = task.startedAt
	go task.reporterLoop()
	task.enqueueStarted()
	return task
}

func (t *Task) enqueueStarted() {
	t.queue.enqueue(t.id, "task_started", true, func(sequence uint64) map[string]any {
		return map[string]any{
			"sequence":   sequence,
			"direction":  t.direction,
			"state":      t.stateString(),
			"phase":      t.phaseString(),
			"started_at": t.startedAt.UTC().Format(time.RFC3339Nano),
		}
	})
}

func (t *Task) bindOptions(options croc.Options) croc.Options {
	options.NoTerminalIO = true
	options.ReceiveFolder = t.savePath
	options.PromptHandler = t.handlePrompt
	options.Progress = croc.ProgressObserver{
		LocalRelay:  func(port string) { t.localRelayPort.Store(&port) },
		Peer:        func(address string) { t.peerAddress.Store(&address) },
		Phase:       t.onPhase,
		Files:       t.onFiles,
		FileStart:   t.onFileStart,
		FileBytes:   t.onFileBytes,
		FileSkipped: t.onFileSkipped,
		FileRenamed: t.onFileRenamed,
		FileCreated: t.onFileCreated,
		Lifecycle:   t.onLifecycle,
	}
	return options
}

func (t *Task) execute(send *sendConfig, receive *receiveConfig, options croc.Options) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t.setError("engine_panic", fmt.Sprintf("engine panic: %v", recovered))
		}
		t.finalize()
	}()
	if send != nil {
		t.runSend(*send, options)
		return
	}
	t.runReceive(*receive, options)
}

func (t *Task) runSend(cfg sendConfig, options croc.Options) {
	t.setPhase("preparing")
	files, emptyFolders, totalFolders, err := croc.GetFilesInfoWithExactExclusionsContext(
		t.ctx, cfg.Files, cfg.ZipFolder, false, cfg.Exclude, cfg.ExcludeFile,
	)
	if err != nil {
		t.setError(classifyError(err), err.Error())
		return
	}
	if len(cfg.Exclude) > 0 {
		files, emptyFolders, totalFolders = filterExcluded(files, emptyFolders, cfg.Exclude)
		if len(files) == 0 && len(emptyFolders) == 0 {
			t.setError("config_error", "no files remain after exclusions")
			return
		}
	}
	t.onFiles(files, totalFolders)
	if t.ctx.Err() != nil {
		t.setError("cancelled", context.Cause(t.ctx).Error())
		return
	}
	t.setPhase("connecting")
	client, err := croc.NewCtx(t.ctx, t.bindOptions(options))
	if err != nil {
		t.setError(classifyError(err), err.Error())
		return
	}
	t.client = client
	t.hashAlgorithm = client.LibraryHashAlgorithm()
	err = client.Send(files, emptyFolders, totalFolders)
	t.handleCrocResult(err)
}

func (t *Task) runReceive(cfg receiveConfig, options croc.Options) {
	t.setPhase("preparing")
	client, err := croc.NewCtx(t.ctx, t.bindOptions(options))
	if err != nil {
		t.setError(classifyError(err), err.Error())
		return
	}
	t.client = client
	t.hashAlgorithm = client.LibraryHashAlgorithm()
	t.setPhase("connecting")
	err = client.Receive()
	t.handleCrocResult(err)
}

func (t *Task) handleCrocResult(err error) {
	if err != nil {
		if t.ctx.Err() != nil || errors.Is(err, context.Canceled) {
			cause := context.Cause(t.ctx)
			if cause == nil {
				cause = err
			}
			t.setError("cancelled", cause.Error())
			return
		}
		t.setError(classifyError(err), err.Error())
		return
	}
	if t.direction == "receive" {
		if verifyErr := t.verifyReceived(); verifyErr != nil {
			if t.ctx.Err() != nil {
				t.setError("cancelled", context.Cause(t.ctx).Error())
				return
			}
			t.setError("verification_failed", verifyErr.Error())
		}
	}
}

func (t *Task) setError(code, message string) {
	t.resultMu.Lock()
	if t.errorText == "" {
		t.errorText = message
		t.errorCode = code
	}
	t.resultMu.Unlock()
}

func classifyError(err error) string {
	if err == nil {
		return ""
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "password mismatch"),
		strings.Contains(text, "pake not successful"),
		strings.Contains(text, "message authentication failed"):
		return "authentication_failed"
	case strings.Contains(text, "no such file"),
		strings.Contains(text, "cannot find"),
		strings.Contains(text, "is a directory"):
		return "source_error"
	case strings.Contains(text, "could not connect"),
		strings.Contains(text, "connection refused"),
		strings.Contains(text, "i/o timeout"),
		strings.Contains(text, "network is unreachable"),
		strings.Contains(text, "no route to host"):
		return "network_error"
	case strings.Contains(text, "context canceled"):
		return "cancelled"
	default:
		return "engine_error"
	}
}

func (t *Task) finalize() {
	if t.finalized.Swap(true) {
		return
	}
	close(t.reporterStop)
	<-t.reporterDone

	state := int32(taskCompleted)
	t.resultMu.Lock()
	errorText := t.errorText
	t.resultMu.Unlock()

	cause := context.Cause(t.ctx)
	if cause != nil && (errors.Is(cause, errTaskCanceledByUser) || errors.Is(cause, context.Canceled)) {
		state = taskCancelled
		if errorText == "" {
			errorText = cause.Error()
			t.setError("cancelled", errorText)
		}
	} else if errorText != "" {
		state = taskFailed
	}
	if state == taskCompleted && t.ctx.Err() != nil {
		state = taskCancelled
		if errorText == "" {
			errorText = context.Cause(t.ctx).Error()
			t.setError("cancelled", errorText)
		}
	}

	finalPhase := "completed"
	switch state {
	case taskCancelled:
		finalPhase = "cancelled"
	case taskFailed:
		finalPhase = "failed"
	}
	t.state.Store(state)
	t.phaseMu.Lock()
	t.phase = finalPhase
	t.phaseMu.Unlock()
	t.resultMu.Lock()
	t.finishedAt = time.Now()
	t.resultMu.Unlock()

	// 终态必须排在该任务所有已入队的阶段/清单事件之后。
	t.eventMu.Lock()
	t.queue.enqueue(t.id, "terminal", true, func(sequence uint64) map[string]any {
		snapshot := t.snapshot()
		return map[string]any{
			"sequence":     sequence,
			"state":        snapshot.State,
			"phase":        snapshot.Phase,
			"started_at":   snapshot.StartedAt,
			"finished_at":  snapshot.FinishedAt,
			"error":        snapshot.Error,
			"error_code":   snapshot.ErrorCode,
			"totals":       snapshot.Totals,
			"transfer":     snapshot.Transfer,
			"verification": snapshot.Verification,
		}
	})
	t.eventMu.Unlock()
	close(t.finished)
}

func (t *Task) cancelTask() {
	t.cancel(errTaskCanceledByUser)
}

func (t *Task) stateString() string {
	switch t.state.Load() {
	case taskCompleted:
		return "completed"
	case taskCancelled:
		return "cancelled"
	case taskFailed:
		return "failed"
	default:
		return "running"
	}
}

func (t *Task) phaseString() string {
	t.phaseMu.Lock()
	defer t.phaseMu.Unlock()
	return t.phase
}

func (t *Task) setPhase(phase string) {
	t.eventMu.Lock()
	defer t.eventMu.Unlock()
	if t.finalized.Load() {
		return
	}
	t.phaseMu.Lock()
	if t.phase == phase {
		t.phaseMu.Unlock()
		return
	}
	previous := t.phase
	t.phase = phase
	t.phaseMu.Unlock()

	t.queue.enqueue(t.id, "phase", true, func(sequence uint64) map[string]any {
		snapshot := t.snapshot()
		return map[string]any{
			"sequence":     sequence,
			"direction":    t.direction,
			"state":        snapshot.State,
			"phase":        phase,
			"previous":     previous,
			"totals":       snapshot.Totals,
			"file":         snapshot.File,
			"transfer":     snapshot.Transfer,
			"verification": snapshot.Verification,
		}
	})
}

func (t *Task) onPhase(phase string) {
	if phase == "" || t.finalized.Load() {
		return
	}
	t.setPhase(phase)
}

func (t *Task) onLifecycle(channelSecured, fileInfoTransferred, recipientRequested, fileTransferred, successful bool) {
	if t.finalized.Load() || successful {
		return
	}
	phase := t.phaseString()
	switch {
	case recipientRequested || fileTransferred:
		if phase != "awaiting_confirmation" && phase != "verifying" {
			t.setPhase("transferring")
		}
	case fileInfoTransferred:
		if phase == "preparing" || phase == "connecting" || phase == "negotiating" {
			t.setPhase("waiting_peer")
		}
	case channelSecured:
		if phase == "preparing" || phase == "connecting" {
			t.setPhase("negotiating")
		}
	}
}

func (t *Task) onFiles(files []croc.FileInfo, totalFolders int) {
	if t.finalized.Load() {
		return
	}
	t.eventMu.Lock()
	defer t.eventMu.Unlock()
	if t.finalized.Load() {
		return
	}
	t.filesMu.Lock()
	if len(t.files) == 0 {
		t.files = make([]*FileState, 0, len(files))
		for index, info := range files {
			state := &FileState{
				Index:        index,
				FolderRemote: info.FolderRemote,
				Name:         info.Name,
				Size:         info.Size,
			}
			state.Path = t.filePathFor(info)
			t.files = append(t.files, state)
		}
	} else {
		// 重连/二次清单只更新元信息，保留真实累计字节。
		for index, info := range files {
			for index >= len(t.files) {
				t.files = append(t.files, &FileState{Index: len(t.files)})
			}
			state := t.files[index]
			state.FolderRemote = info.FolderRemote
			state.Name = info.Name
			state.Size = info.Size
			if state.Path == "" {
				state.Path = t.filePathFor(info)
			}
		}
	}
	t.totalFolders = totalFolders
	states := make([]*FileState, len(t.files))
	copy(states, t.files)
	t.filesMu.Unlock()

	var bytesTotal int64
	for _, state := range states {
		bytesTotal += state.Size
	}
	t.totalBytes.Store(bytesTotal)
	t.totalFiles.Store(int32(len(states)))

	// 文件清单可能很大，事件只在清单变化时发送一次；完整清单也在快照中。
	t.queue.enqueue(t.id, "files", true, func(sequence uint64) map[string]any {
		list := make([]map[string]any, 0, len(states))
		for _, state := range states {
			list = append(list, fileJSON(t.fileSnapshot(state)))
		}
		return map[string]any{
			"sequence":      sequence,
			"direction":     t.direction,
			"state":         t.stateString(),
			"phase":         t.phaseString(),
			"files":         list,
			"total_folders": totalFolders,
			"totals":        t.totalsSnapshot(),
		}
	})
}

func (t *Task) filePathFor(info croc.FileInfo) string {
	if t.direction == "receive" {
		return filepath.Join(t.savePath, filepath.FromSlash(info.FolderRemote), filepath.FromSlash(strings.ReplaceAll(info.Name, "\\", "/")))
	}
	if info.FolderSource == "" {
		return info.Name
	}
	return filepath.Join(info.FolderSource, info.Name)
}

func (t *Task) onFileStart(index int) {
	if t.finalized.Load() {
		return
	}
	t.filesMu.RLock()
	valid := index >= 0 && index < len(t.files)
	t.filesMu.RUnlock()
	if !valid {
		return
	}
	t.currentIndex.Store(int64(index))
	phase := t.phaseString()
	if phase != "verifying" && phase != "awaiting_confirmation" {
		t.setPhase("transferring")
	}
}

func (t *Task) onFileBytes(index int, delta int64, session bool) {
	if delta <= 0 || t.finalized.Load() {
		return
	}
	t.filesMu.RLock()
	var state *FileState
	var size int64
	if index >= 0 && index < len(t.files) {
		state = t.files[index]
		size = state.Size
	}
	t.filesMu.RUnlock()
	if state == nil {
		return
	}
	actual := delta
	for {
		old := state.Done.Load()
		next := old + delta
		if next > size {
			next = size
		}
		actual = next - old
		if actual <= 0 {
			return
		}
		if state.Done.CompareAndSwap(old, next) {
			break
		}
	}
	if !session {
		state.Reused.Add(actual)
	}
	if session {
		state.Session.Add(actual)
		t.sessionBytes.Add(actual)
		now := time.Now()
		t.speedMu.Lock()
		t.lastBytesChange = now
		t.speedMu.Unlock()
	}
	t.totalDone.Add(actual)
	t.currentIndex.Store(int64(index))
	if session {
		t.maybeAdvanceAfterBytes()
	}
}

func (t *Task) maybeAdvanceAfterBytes() {
	total := t.totalBytes.Load()
	if total <= 0 || t.totalDone.Load() < total {
		return
	}
	phase := t.phaseString()
	if phase != "transferring" {
		return
	}
	if t.direction == "receive" {
		t.setPhase("verifying")
	} else {
		t.setPhase("awaiting_confirmation")
	}
}

func (t *Task) onFileSkipped(index int, reason string) {
	t.filesMu.RLock()
	var state *FileState
	var size int64
	if index >= 0 && index < len(t.files) {
		state = t.files[index]
		size = state.Size
	}
	t.filesMu.RUnlock()
	if state == nil {
		return
	}
	// Croc 在收尾阶段可能再次执行 preflight，并把刚落盘且哈希一致的文件
	// 标记为 unchanged。已经在本次会话传输完成的文件不能被误标为跳过。
	phase := t.phaseString()
	alreadyTransferred := state.Session.Load() > 0 ||
		(size == 0 && (phase == "transferring" || phase == "verifying" || phase == "awaiting_confirmation"))
	if state.Done.Load() >= size && alreadyTransferred {
		return
	}
	if !state.Skipped.Swap(true) {
		t.skippedFiles.Add(1)
	}
	value := reason
	state.skippedReason.Store(&value)
}

func (t *Task) onFileCreated(index int, file *os.File) {
	identity := createdFileIdentity(file)
	if identity == "" {
		return
	}
	t.filesMu.Lock()
	defer t.filesMu.Unlock()
	if index >= 0 && index < len(t.files) {
		t.files[index].CreatedIdentity = identity
	}
}

func (t *Task) onFileRenamed(index int, newName string) {
	t.filesMu.Lock()
	if index >= 0 && index < len(t.files) {
		state := t.files[index]
		state.Name = newName
		state.Path = filepath.Join(t.savePath, filepath.FromSlash(state.FolderRemote), filepath.FromSlash(strings.ReplaceAll(newName, "\\", "/")))
	}
	t.filesMu.Unlock()
}

func (t *Task) handlePrompt(ctx context.Context, request croc.PromptRequest) (croc.PromptAnswer, error) {
	if t.finalized.Load() {
		return croc.PromptAnswer{}, context.Canceled
	}
	promptID := fmt.Sprintf("%d-%d", t.id, t.promptSeq.Add(1))
	response := make(chan bool, 1)
	pending := &pendingPrompt{request: request, response: response}
	t.promptMu.Lock()
	if t.prompts == nil {
		t.prompts = make(map[string]*pendingPrompt)
	}
	t.prompts[promptID] = pending
	t.promptMu.Unlock()

	t.setPhase("awaiting_confirmation")
	ok := t.queue.enqueue(t.id, "prompt", true, func(sequence uint64) map[string]any {
		snapshot := t.snapshot()
		file := map[string]any{
			"index":       request.FileIndex,
			"name":        request.FileName,
			"destination": request.Destination,
		}
		return map[string]any{
			"sequence":        sequence,
			"direction":       t.direction,
			"state":           snapshot.State,
			"phase":           "awaiting_confirmation",
			"prompt_id":       promptID,
			"prompt_kind":     string(request.Kind),
			"file":            file,
			"files_count":     snapshot.Totals.Files,
			"bytes_total":     snapshot.Totals.BytesTotal,
			"peer_machine_id": request.PeerID,
			"resume_percent":  request.ResumePercent,
			"default":         defaultDecision(request.DefaultYes),
			"totals":          snapshot.Totals,
			"transfer":        snapshot.Transfer,
		}
	})
	if !ok {
		t.removePrompt(promptID)
		return croc.PromptAnswer{}, errors.New("event queue is full; prompt cannot be delivered")
	}

	var timer *time.Timer
	var timeout <-chan time.Time
	if t.promptTimeout > 0 {
		timer = time.NewTimer(t.promptTimeout)
		timeout = timer.C
	}
	if timer != nil {
		defer timer.Stop()
	}
	select {
	case accepted := <-response:
		return croc.PromptAnswer{Accepted: accepted}, nil
	case <-ctx.Done():
		t.removePrompt(promptID)
		return croc.PromptAnswer{}, ctx.Err()
	case <-t.ctx.Done():
		t.removePrompt(promptID)
		return croc.PromptAnswer{}, context.Canceled
	case <-timeout:
		t.removePrompt(promptID)
		return croc.PromptAnswer{}, errPromptTimeout
	}
}

func defaultDecision(defaultYes bool) string {
	if defaultYes {
		return "accept"
	}
	return "reject"
}

func (t *Task) removePrompt(promptID string) {
	t.promptMu.Lock()
	delete(t.prompts, promptID)
	t.promptMu.Unlock()
}

// RespondPrompt 处理 C++ 的回答。同一个 prompt 只能成功处理一次。
func (t *Task) RespondPrompt(promptID string, decision int32) error {
	t.promptMu.Lock()
	pending := t.prompts[promptID]
	if pending == nil {
		t.promptMu.Unlock()
		return errPromptNotFound
	}
	accepted, err := decisionAccepted(pending.request.Kind, decision)
	if err != nil {
		t.promptMu.Unlock()
		return err
	}
	delete(t.prompts, promptID)
	t.promptMu.Unlock()
	select {
	case pending.response <- accepted:
	default:
	}
	return nil
}

func decisionAccepted(kind croc.PromptKind, decision int32) (bool, error) {
	switch kind {
	case croc.PromptReceiveAccept, croc.PromptSendApproval:
		switch decision {
		case DecisionAccept:
			return true, nil
		case DecisionReject, DecisionSkip:
			return false, nil
		default:
			return false, errInvalidDecision
		}
	case croc.PromptOverwrite:
		switch decision {
		case DecisionOverwrite, DecisionAccept, DecisionResume:
			return true, nil
		case DecisionReject, DecisionSkip:
			return false, nil
		default:
			return false, errInvalidDecision
		}
	case croc.PromptFolderOverwrite:
		switch decision {
		case DecisionOverwrite, DecisionAccept:
			return true, nil
		case DecisionReject, DecisionSkip:
			return false, nil
		default:
			return false, errInvalidDecision
		}
	default:
		return false, errInvalidDecision
	}
}

func (t *Task) verifyReceived() error {
	client := t.client
	if client == nil {
		return errors.New("receive client is not available")
	}
	files := client.LibraryFiles()
	algorithm := client.LibraryHashAlgorithm()
	t.bundleUsed = client.LibraryBundleUsed()
	t.hashAlgorithm = algorithm
	if algorithm == "" {
		algorithm = "xxhash"
	}
	t.filesMu.RLock()
	states := make([]*FileState, len(t.files))
	copy(states, t.files)
	t.filesMu.RUnlock()
	if len(files) != len(states) {
		return fmt.Errorf("received manifest mismatch: %d files, %d states", len(files), len(states))
	}

	streaming := algorithm == "xxhash" || algorithm == "md5" || algorithm == "highway"
	t.verifyMu.Lock()
	t.verifyBytesKnown = streaming
	t.verifyMu.Unlock()

	var total int64
	var totalFiles int32
	for index, info := range files {
		state := states[index]
		if state.Skipped.Load() || info.TempFile || info.Symlink != "" || len(info.Hash) == 0 {
			continue
		}
		total += info.Size
		totalFiles++
	}
	t.verifyTotal.Store(total)
	t.verifyFilesTotal.Store(totalFiles)

	if totalFiles == 0 {
		status := "unavailable"
		for _, state := range states {
			if state.Skipped.Load() {
				status = "skipped"
				break
			}
		}
		t.setVerifyStatus(status)
		return nil
	}

	t.setPhase("verifying")
	t.setVerifyStatus("running")

	var failures []string
	for index, info := range files {
		if err := t.ctx.Err(); err != nil {
			return err
		}
		state := states[index]
		if state.Skipped.Load() {
			continue
		}
		if info.TempFile || info.Symlink != "" || len(info.Hash) == 0 {
			state.Unavailable.Store(true)
			continue
		}
		state.Verifying.Store(true)
		got, err := t.computeFileHash(state.Path, algorithm)
		state.Verifying.Store(false)
		if err != nil {
			state.Failed.Store(true)
			t.verifyFilesFailed.Add(1)
			failures = append(failures, fmt.Sprintf("%s: %v", info.Name, err))
			continue
		}
		if !bytes.Equal(got, info.Hash) {
			state.Failed.Store(true)
			t.verifyFilesFailed.Add(1)
			failures = append(failures, fmt.Sprintf("%s: hash mismatch", info.Name))
			continue
		}
		state.Verified.Store(true)
		t.verifyFilesDone.Add(1)
	}
	if t.verifyFilesFailed.Load() > 0 {
		t.setVerifyStatus("failed")
		return fmt.Errorf("verification failed for %d file(s): %s", len(failures), strings.Join(failures, "; "))
	}
	t.setVerifyStatus("passed")
	return nil
}

func (t *Task) setVerifyStatus(status string) {
	t.verifyMu.Lock()
	t.verifyStatus = status
	t.verifyMu.Unlock()
}

func (t *Task) computeFileHash(filename, algorithm string) ([]byte, error) {
	var hasher hash.Hash
	switch algorithm {
	case "xxhash":
		hasher = xxhash.New()
	case "md5":
		hasher = md5.New()
	case "highway":
		highway, err := highwayhash.New(highwayKey)
		if err != nil {
			return nil, err
		}
		hasher = highway
	default:
		// imohash/imohash-v2 是采样哈希，无法给出按字节的准确百分比；
		// 只报告真实阶段与最终结果，不伪造计量。
		return utils.HashFileCtx(t.ctx, filename, algorithm, false)
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	writer := io.MultiWriter(hasher, &verifyProgressWriter{task: t})
	if _, err = io.CopyBuffer(writer, &contextReader{ctx: t.ctx, reader: file}, make([]byte, 256*1024)); err != nil {
		return nil, err
	}
	return hasher.Sum(nil), nil
}

type verifyProgressWriter struct {
	task *Task
}

func (w *verifyProgressWriter) Write(p []byte) (int, error) {
	w.task.verifyDone.Add(int64(len(p)))
	now := time.Now()
	w.task.speedMu.Lock()
	w.task.lastVerifyChange = now
	w.task.speedMu.Unlock()
	return len(p), nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func (t *Task) reporterLoop() {
	defer close(t.reporterDone)
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-t.reporterStop:
			return
		case now := <-ticker.C:
			if t.finalized.Load() {
				return
			}
			t.sampleSpeeds(now)
			t.emitProgressIfNeeded(now)
		}
	}
}

func (t *Task) sampleSpeeds(now time.Time) {
	session := t.sessionBytes.Load()
	verifyDone := t.verifyDone.Load()
	t.speedMu.Lock()
	dt := now.Sub(t.lastSampleTime).Seconds()
	if dt <= 0 {
		dt = 0.001
	}
	delta := session - t.lastSampleBytes
	if delta > 0 {
		instant := float64(delta) / dt
		if t.speedBps == 0 {
			t.speedBps = instant
		} else {
			t.speedBps = 0.35*instant + 0.65*t.speedBps
		}
		t.lastBytesChange = now
	} else if now.Sub(t.lastBytesChange) > 2*time.Second {
		t.speedBps *= 0.4
		if t.speedBps < 1024 {
			t.speedBps = 0
		}
	}
	verifyDelta := verifyDone - t.lastVerifySample
	if verifyDelta > 0 {
		instant := float64(verifyDelta) / dt
		if t.verifySpeedBps == 0 {
			t.verifySpeedBps = instant
		} else {
			t.verifySpeedBps = 0.35*instant + 0.65*t.verifySpeedBps
		}
		t.lastVerifyChange = now
	} else if now.Sub(t.lastVerifyChange) > 2*time.Second {
		t.verifySpeedBps *= 0.4
		if t.verifySpeedBps < 1024 {
			t.verifySpeedBps = 0
		}
	}
	t.lastSampleTime = now
	t.lastSampleBytes = session
	t.lastVerifySample = verifyDone

	phase := t.phaseString()
	if phase == "transferring" && t.speedBps > 0 {
		remaining := t.remainingBytes()
		if remaining > 0 {
			eta := float64(remaining) / t.speedBps
			t.etaSeconds = &eta
		} else {
			t.etaSeconds = nil
		}
	} else {
		t.etaSeconds = nil
	}
	t.speedMu.Unlock()
}

func (t *Task) remainingBytes() int64 {
	t.filesMu.RLock()
	defer t.filesMu.RUnlock()
	var remaining int64
	for _, state := range t.files {
		if state.Skipped.Load() {
			continue
		}
		left := state.Size - state.Done.Load()
		if left > 0 {
			remaining += left
		}
	}
	return remaining
}

func (t *Task) emitProgressIfNeeded(now time.Time) {
	session := t.sessionBytes.Load()
	verifyDone := t.verifyDone.Load()
	phase := t.phaseString()
	changed := session != t.lastEmitSession || verifyDone != t.lastEmitVerify || phase != t.lastEmitPhase
	idle := now.Sub(t.lastEmitTime) >= time.Second
	if !changed && !idle {
		return
	}
	t.lastEmitSession = session
	t.lastEmitVerify = verifyDone
	t.lastEmitPhase = phase
	t.lastEmitTime = now
	t.emitProgress()
}

func (t *Task) emitProgress() {
	if t.finalized.Load() {
		return
	}
	t.queue.enqueue(t.id, "progress", false, func(sequence uint64) map[string]any {
		snapshot := t.snapshot()
		return map[string]any{
			"sequence":     sequence,
			"direction":    t.direction,
			"state":        snapshot.State,
			"phase":        snapshot.Phase,
			"totals":       snapshot.Totals,
			"file":         snapshot.File,
			"transfer":     snapshot.Transfer,
			"verification": snapshot.Verification,
		}
	})
}

func (t *Task) totalsSnapshot() totalsSnapshot {
	t.filesMu.RLock()
	defer t.filesMu.RUnlock()
	states := make([]*FileState, len(t.files))
	copy(states, t.files)
	totals := totalsSnapshot{
		Files:        len(states),
		BytesTotal:   t.totalBytes.Load(),
		BytesDone:    t.totalDone.Load(),
		SessionBytes: t.sessionBytes.Load(),
		Folders:      t.totalFolders,
	}
	for _, state := range states {
		if state.Skipped.Load() {
			totals.FilesSkipped++
			continue
		}
		if state.Failed.Load() {
			totals.FilesFailed++
		}
		if state.Done.Load() >= state.Size {
			totals.FilesCompleted++
		}
	}
	return totals
}

func (t *Task) fileStatus(state *FileState) string {
	if state.Skipped.Load() {
		if reason := state.skippedReason.Load(); reason != nil && *reason != "" {
			return *reason
		}
		return "skipped"
	}
	if state.Failed.Load() {
		return "failed"
	}
	if state.Unavailable.Load() {
		return "unavailable"
	}
	if state.Verified.Load() {
		return "verified"
	}
	if state.Verifying.Load() {
		return "verifying"
	}
	if state.Done.Load() >= state.Size {
		return "completed"
	}
	if state.Done.Load() > 0 {
		return "transferring"
	}
	return "pending"
}

func (t *Task) fileSnapshot(state *FileState) fileSnapshot {
	// Name/Path/Size 由 onFiles/onFileRenamed 在 filesMu 下更新；进度快照
	// 与网络协程并发读取时必须保持一致。
	t.filesMu.RLock()
	defer t.filesMu.RUnlock()
	return fileSnapshot{
		Index:           state.Index,
		Name:            state.Name,
		Path:            state.Path,
		Size:            state.Size,
		BytesDone:       min(max(state.Done.Load(), 0), max(state.Size, 0)),
		SessionBytes:    state.Session.Load(),
		ReusedBytes:     state.Reused.Load(),
		Status:          t.fileStatus(state),
		CreatedIdentity: state.CreatedIdentity,
	}
}

func (t *Task) snapshot() snapshotDocument {
	t.filesMu.RLock()
	states := make([]*FileState, len(t.files))
	copy(states, t.files)
	t.filesMu.RUnlock()

	phase := t.phaseString()
	state := t.stateString()
	t.resultMu.Lock()
	errorText := t.errorText
	errorCode := t.errorCode
	finishedAt := t.finishedAt
	t.resultMu.Unlock()

	t.speedMu.Lock()
	speed := t.speedBps
	eta := t.etaSeconds
	verifySpeed := t.verifySpeedBps
	t.speedMu.Unlock()
	if state != "running" {
		speed = 0
		eta = nil
		verifySpeed = 0
	}

	document := snapshotDocument{
		SchemaVersion: SchemaVersion,
		TaskID:        t.id,
		Direction:     t.direction,
		State:         state,
		Phase:         phase,
		StartedAt:     t.startedAt.UTC().Format(time.RFC3339Nano),
		Totals:        t.totalsSnapshot(),
		Files:         make([]fileSnapshot, 0, len(states)),
	}
	if !finishedAt.IsZero() {
		document.FinishedAt = stringPointer(finishedAt.UTC().Format(time.RFC3339Nano))
	}
	if errorText != "" {
		document.Error = stringPointer(errorText)
		document.ErrorCode = stringPointer(errorCode)
	}
	if port := t.localRelayPort.Load(); port != nil {
		document.LocalRelayPort = *port
	}
	if address := t.peerAddress.Load(); address != nil {
		document.PeerAddress = *address
	}
	document.Transfer = transferSnapshot{
		BytesDone:    t.totalDone.Load(),
		BytesTotal:   t.totalBytes.Load(),
		SessionBytes: t.sessionBytes.Load(),
	}
	if document.Transfer.BytesTotal > 0 {
		percent := float64(document.Transfer.BytesDone) / float64(document.Transfer.BytesTotal) * 100
		percent = min(max(percent, 0), 100)
		document.Transfer.Percent = floatPointer(percent)
	}
	if phase == "transferring" && speed > 0 {
		document.Transfer.SpeedBPS = floatPointer(speed)
	}
	if eta != nil && phase == "transferring" {
		document.Transfer.ETASeconds = floatPointer(*eta)
	}

	t.verifyMu.Lock()
	verifyStatus := t.verifyStatus
	bytesKnown := t.verifyBytesKnown
	t.verifyMu.Unlock()
	document.Verification = verificationSnapshot{
		Status:      verifyStatus,
		FilesTotal:  int(t.verifyFilesTotal.Load()),
		FilesDone:   int(t.verifyFilesDone.Load()),
		FilesFailed: int(t.verifyFilesFailed.Load()),
	}
	if bytesKnown {
		document.Verification.BytesDone = int64Pointer(t.verifyDone.Load())
		document.Verification.BytesTotal = int64Pointer(t.verifyTotal.Load())
		if phase == "verifying" && verifySpeed > 0 {
			document.Verification.SpeedBPS = floatPointer(verifySpeed)
		}
	}

	for _, state := range states {
		file := t.fileSnapshot(state)
		document.Files = append(document.Files, file)
	}
	current := int(t.currentIndex.Load())
	if current >= 0 && current < len(document.Files) {
		file := document.Files[current]
		document.File = &file
	} else if len(document.Files) > 0 {
		file := document.Files[0]
		document.File = &file
	}
	return document
}

func (t *Task) snapshotJSON() ([]byte, error) {
	return jsonMarshal(t.snapshot())
}

func filterExcluded(files []croc.FileInfo, empty []croc.FileInfo, exclusions []string) ([]croc.FileInfo, []croc.FileInfo, int) {
	keepFile := func(info croc.FileInfo) bool {
		joined := strings.ToLower(path.Join(strings.ToLower(info.FolderRemote), strings.ToLower(info.Name)))
		for _, exclusion := range exclusions {
			if strings.Contains(joined, strings.ToLower(exclusion)) {
				return false
			}
		}
		return true
	}
	filteredFiles := make([]croc.FileInfo, 0, len(files))
	for _, info := range files {
		if keepFile(info) {
			filteredFiles = append(filteredFiles, info)
		}
	}
	filteredEmpty := make([]croc.FileInfo, 0, len(empty))
	for _, info := range empty {
		if keepFile(info) {
			filteredEmpty = append(filteredEmpty, info)
		}
	}
	folderSet := make(map[string]bool)
	for _, info := range filteredFiles {
		folderSet[info.FolderRemote] = true
	}
	for _, info := range filteredEmpty {
		folderSet[info.FolderRemote] = true
	}
	return filteredFiles, filteredEmpty, len(folderSet)
}
