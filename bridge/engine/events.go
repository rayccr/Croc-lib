package engine

import (
	"encoding/json"
	"sync"
	"time"
)

func jsonMarshal(value any) ([]byte, error) {
	return json.Marshal(value)
}

const (
	// SchemaVersion 是事件和快照 JSON 的版本号。
	SchemaVersion = 1
	// DefaultEventQueueCapacity 是全局事件队列上限。进度事件会按任务合并，
	// 因此正常情况下队列中每个任务最多保留一条待取进度事件。
	DefaultEventQueueCapacity = 8192
)

type fileSnapshot struct {
	Index           int    `json:"index"`
	Name            string `json:"name"`
	Path            string `json:"path,omitempty"`
	Size            int64  `json:"size"`
	BytesDone       int64  `json:"bytes_done"`
	SessionBytes    int64  `json:"session_bytes"`
	ReusedBytes     int64  `json:"reused_bytes"`
	Status          string `json:"status"`
	CreatedIdentity string `json:"created_file_identity,omitempty"`
}

type totalsSnapshot struct {
	Files          int   `json:"files"`
	FilesCompleted int   `json:"files_completed"`
	FilesSkipped   int   `json:"files_skipped"`
	FilesFailed    int   `json:"files_failed"`
	Folders        int   `json:"folders"`
	BytesTotal     int64 `json:"bytes_total"`
	BytesDone      int64 `json:"bytes_done"`
	SessionBytes   int64 `json:"session_bytes"`
}

type transferSnapshot struct {
	BytesDone    int64    `json:"bytes_done"`
	BytesTotal   int64    `json:"bytes_total"`
	SessionBytes int64    `json:"session_bytes"`
	SpeedBPS     *float64 `json:"speed_bps"`
	ETASeconds   *float64 `json:"eta_seconds"`
	Percent      *float64 `json:"percent"`
}

type verificationSnapshot struct {
	Status      string   `json:"status"`
	BytesDone   *int64   `json:"bytes_done"`
	BytesTotal  *int64   `json:"bytes_total"`
	SpeedBPS    *float64 `json:"speed_bps"`
	FilesTotal  int      `json:"files_total"`
	FilesDone   int      `json:"files_done"`
	FilesFailed int      `json:"files_failed"`
}

type snapshotDocument struct {
	LocalRelayPort string               `json:"local_relay_port,omitempty"`
	PeerAddress    string               `json:"peer_address,omitempty"`
	SchemaVersion  int                  `json:"schema_version"`
	TaskID         uint64               `json:"task_id"`
	Direction      string               `json:"direction"`
	State          string               `json:"state"`
	Phase          string               `json:"phase"`
	StartedAt      string               `json:"started_at"`
	FinishedAt     *string              `json:"finished_at"`
	Error          *string              `json:"error"`
	ErrorCode      *string              `json:"error_code"`
	Totals         totalsSnapshot       `json:"totals"`
	File           *fileSnapshot        `json:"file"`
	Transfer       transferSnapshot     `json:"transfer"`
	Verification   verificationSnapshot `json:"verification"`
	Files          []fileSnapshot       `json:"files"`
}

type queuedEvent struct {
	taskID   uint64
	kind     string
	critical bool
	data     []byte
}

type eventQueue struct {
	mu      sync.Mutex
	events  []queuedEvent
	max     int
	seq     uint64
	dropped uint64
}

func newEventQueue(capacity int) *eventQueue {
	if capacity <= 0 {
		capacity = DefaultEventQueueCapacity
	}
	return &eventQueue{max: capacity}
}

// enqueue 在队列锁内分配 sequence 并序列化事件。build 不得反向调用会长时间
// 阻塞的任务/网络路径。关键事件优先保留，进度事件按任务合并。
func (q *eventQueue) enqueue(taskID uint64, kind string, critical bool, build func(sequence uint64) map[string]any) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	if !critical {
		// 同一任务只保留尚未被取走的最新进度事件。
		for index := len(q.events) - 1; index >= 0; index-- {
			if q.events[index].taskID == taskID && !q.events[index].critical && q.events[index].kind == kind {
				q.events = append(q.events[:index], q.events[index+1:]...)
				break
			}
		}
	}
	if len(q.events) >= q.max {
		dropped := false
		// 先丢弃任意任务的旧进度事件，保留生命周期/提示/终态事件。
		for index := 0; index < len(q.events); index++ {
			if !q.events[index].critical {
				q.events = append(q.events[:index], q.events[index+1:]...)
				dropped = true
				break
			}
		}
		if !dropped {
			q.dropped++
			return false
		}
	}
	q.seq++
	payload := build(q.seq)
	if payload == nil {
		q.dropped++
		return false
	}
	payload["schema_version"] = SchemaVersion
	payload["sequence"] = q.seq
	payload["event"] = kind
	payload["task_id"] = taskID
	payload["timestamp"] = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.Marshal(payload)
	if err != nil {
		q.dropped++
		return false
	}
	q.events = append(q.events, queuedEvent{taskID: taskID, kind: kind, critical: critical, data: data})
	return true
}

// peek 返回队首事件；缓冲区不足时不弹出，保证调用方放大缓冲后仍能取到。
func (q *eventQueue) peek() ([]byte, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.events) == 0 {
		return nil, false
	}
	return q.events[0].data, true
}

// poll 在同一个锁内完成“查看—复制—弹出”，避免并发轮询时取到错误事件。
// write 返回 false 表示调用方缓冲区不足；此时事件保留在队列中。
func (q *eventQueue) poll(write func([]byte) bool) (int32, int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.events) == 0 {
		return CodeNoEvent, 0
	}
	data := q.events[0].data
	if !write(data) {
		return CodeBufferTooSmall, len(data) + 1
	}
	q.events = q.events[1:]
	return CodeEventAvailable, 0
}

func (q *eventQueue) droppedCount() uint64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.dropped
}

func (q *eventQueue) reset() {
	q.mu.Lock()
	q.events = nil
	q.seq = 0
	q.dropped = 0
	q.mu.Unlock()
}

func stringPointer(value string) *string {
	return &value
}

func floatPointer(value float64) *float64 {
	return &value
}

func int64Pointer(value int64) *int64 {
	return &value
}

func fileJSON(file fileSnapshot) map[string]any {
	return map[string]any{
		"index":         file.Index,
		"name":          file.Name,
		"path":          file.Path,
		"size":          file.Size,
		"bytes_done":    file.BytesDone,
		"session_bytes": file.SessionBytes,
		"reused_bytes":  file.ReusedBytes,
		"status":        file.Status,
	}
}
