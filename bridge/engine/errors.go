package engine

import (
	"errors"
	"sync"
)

// C ABI 返回码。必须与 bridge/include/croc_engine_api.h 保持一致。
const (
	CodeOK              int32 = 0
	CodeEventAvailable  int32 = 1
	CodeNoEvent         int32 = 0
	CodeInvalidArgument int32 = -1
	CodeNotInitialized  int32 = -2
	CodeAlreadyInit     int32 = -3
	CodeTaskNotFound    int32 = -4
	CodeBufferTooSmall  int32 = -5
	CodeInvalidJSON     int32 = -6
	CodeInvalidConfig   int32 = -7
	CodeTaskBusy        int32 = -8
	CodePromptNotFound  int32 = -9
	CodeInvalidDecision int32 = -10
	CodeShuttingDown    int32 = -11
	CodeInternal        int32 = -12
	CodeTimeout         int32 = -13
)

// 提示决定值。必须与公开头文件保持一致。
const (
	DecisionReject    int32 = 0
	DecisionAccept    int32 = 1
	DecisionOverwrite int32 = 2
	DecisionSkip      int32 = 3
	DecisionResume    int32 = 4
)

var (
	errNotInitialized  = errors.New("croc engine is not initialized")
	errShuttingDown    = errors.New("croc engine is shutting down")
	errTaskNotFound    = errors.New("task not found")
	errTaskBusy        = errors.New("task is still running")
	errPromptNotFound  = errors.New("prompt not found or already answered")
	errInvalidDecision = errors.New("invalid prompt decision")
	errPromptTimeout   = errors.New("prompt timed out")
	errPromptCanceled  = errors.New("prompt canceled")
)

var lastErrorState struct {
	sync.Mutex
	message string
}

// SetLastError 保存最近一次 API 错误文本，供 Croc_GetLastError 读取。
// 这是进程级“最近错误”提示，不替代每个函数的错误码和错误缓冲区。
func SetLastError(message string) {
	lastErrorState.Lock()
	lastErrorState.message = message
	lastErrorState.Unlock()
}

// LastError 返回最近一次 API 错误文本。
func LastError() string {
	lastErrorState.Lock()
	defer lastErrorState.Unlock()
	return lastErrorState.message
}

func apiError(code int32, message string) (int32, string) {
	if message != "" {
		SetLastError(message)
	}
	return code, message
}
