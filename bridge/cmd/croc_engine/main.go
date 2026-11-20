package main

/*
#include <stdint.h>
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"unsafe"

	"crocengine/engine"
)

var globalEngine = engine.New()

// C.CString 分配的内存在进程生命周期内保留。版本字符串是不可变常量，
// 调用方不得释放，也不应在多线程中写入。
var (
	apiVersionCString    = C.CString(engine.APIVersion)
	engineVersionCString = C.CString(engine.EngineVersion)
)

func main() {}

func writeCString(destination *C.char, capacity C.int32_t, text string) {
	if destination == nil || capacity <= 0 {
		return
	}
	length := len(text)
	if length > int(capacity)-1 {
		length = int(capacity) - 1
	}
	dst := unsafe.Slice((*byte)(unsafe.Pointer(destination)), int(capacity))
	copy(dst, []byte(text)[:length])
	dst[length] = 0
}

func writeStartError(buffer *C.char, capacity C.int32_t, message string) {
	writeCString(buffer, capacity, message)
}

// copyJSON 把 Go 字节切片复制到调用方缓冲区。缓冲区不足时不写入、不破坏
// 原内容，并通过 required 返回包含 NUL 的所需字节数。
func copyJSON(data []byte, buffer *C.char, capacity C.int32_t, required *C.int32_t) int32 {
	needed := C.int32_t(len(data) + 1)
	if required != nil {
		*required = needed
	}
	if buffer == nil || capacity <= 0 || capacity < needed {
		return engine.CodeBufferTooSmall
	}
	dst := unsafe.Slice((*byte)(unsafe.Pointer(buffer)), int(capacity))
	copy(dst, data)
	dst[len(data)] = 0
	if required != nil {
		*required = C.int32_t(len(data))
	}
	return engine.CodeOK
}

func recoverAPI(buffer *C.char, capacity C.int32_t, result *C.int32_t) {
	if recovered := recover(); recovered != nil {
		message := fmt.Sprintf("panic in croc engine API: %v", recovered)
		engine.SetLastError(message)
		writeStartError(buffer, capacity, message)
		if result != nil {
			*result = C.int32_t(engine.CodeInternal)
		}
	}
}

//export Croc_GetApiVersion
func Croc_GetApiVersion() *C.char {
	return apiVersionCString
}

//export Croc_GetEngineVersion
func Croc_GetEngineVersion() *C.char {
	return engineVersionCString
}

//export Croc_Initialize
func Croc_Initialize() (result C.int32_t) {
	defer recoverAPI(nil, 0, &result)
	return C.int32_t(globalEngine.Initialize())
}

//export Croc_Shutdown
func Croc_Shutdown() (result C.int32_t) {
	defer recoverAPI(nil, 0, &result)
	code, err := globalEngine.Shutdown()
	if err != nil {
		engine.SetLastError(err.Error())
	}
	return C.int32_t(code)
}

//export Croc_StartSend
func Croc_StartSend(configJSON *C.char, taskID *C.uint64_t, errorBuffer *C.char, errorCapacity C.int32_t) (result C.int32_t) {
	defer recoverAPI(errorBuffer, errorCapacity, &result)
	if configJSON == nil || taskID == nil {
		message := "config JSON and task id output are required"
		engine.SetLastError(message)
		writeStartError(errorBuffer, errorCapacity, message)
		return C.int32_t(engine.CodeInvalidArgument)
	}
	id, code, err := globalEngine.StartSend([]byte(C.GoString(configJSON)))
	if err != nil {
		engine.SetLastError(err.Error())
		writeStartError(errorBuffer, errorCapacity, err.Error())
		return C.int32_t(code)
	}
	*taskID = C.uint64_t(id)
	if errorBuffer != nil && errorCapacity > 0 {
		writeStartError(errorBuffer, errorCapacity, "")
	}
	return C.int32_t(engine.CodeOK)
}

//export Croc_StartReceive
func Croc_StartReceive(configJSON *C.char, taskID *C.uint64_t, errorBuffer *C.char, errorCapacity C.int32_t) (result C.int32_t) {
	defer recoverAPI(errorBuffer, errorCapacity, &result)
	if configJSON == nil || taskID == nil {
		message := "config JSON and task id output are required"
		engine.SetLastError(message)
		writeStartError(errorBuffer, errorCapacity, message)
		return C.int32_t(engine.CodeInvalidArgument)
	}
	id, code, err := globalEngine.StartReceive([]byte(C.GoString(configJSON)))
	if err != nil {
		engine.SetLastError(err.Error())
		writeStartError(errorBuffer, errorCapacity, err.Error())
		return C.int32_t(code)
	}
	*taskID = C.uint64_t(id)
	if errorBuffer != nil && errorCapacity > 0 {
		writeStartError(errorBuffer, errorCapacity, "")
	}
	return C.int32_t(engine.CodeOK)
}

//export Croc_CancelTask
func Croc_CancelTask(taskID C.uint64_t) (result C.int32_t) {
	defer recoverAPI(nil, 0, &result)
	code, err := globalEngine.CancelTask(uint64(taskID))
	if err != nil {
		engine.SetLastError(err.Error())
	}
	return C.int32_t(code)
}

//export Croc_IsTaskFinished
func Croc_IsTaskFinished(taskID C.uint64_t, finished *C.int32_t) (result C.int32_t) {
	defer recoverAPI(nil, 0, &result)
	if finished == nil {
		engine.SetLastError("finished output is required")
		return C.int32_t(engine.CodeInvalidArgument)
	}
	isFinished, code, err := globalEngine.IsTaskFinished(uint64(taskID))
	if err != nil {
		engine.SetLastError(err.Error())
		return C.int32_t(code)
	}
	if isFinished {
		*finished = 1
	} else {
		*finished = 0
	}
	return C.int32_t(engine.CodeOK)
}

//export Croc_GetTaskSnapshot
func Croc_GetTaskSnapshot(taskID C.uint64_t, buffer *C.char, capacity C.int32_t, required *C.int32_t) (result C.int32_t) {
	defer recoverAPI(nil, 0, &result)
	if required == nil {
		engine.SetLastError("required output is required")
		return C.int32_t(engine.CodeInvalidArgument)
	}
	data, code, err := globalEngine.TaskSnapshot(uint64(taskID))
	if err != nil {
		engine.SetLastError(err.Error())
		return C.int32_t(code)
	}
	return C.int32_t(copyJSON(data, buffer, capacity, required))
}

//export Croc_PollEvent
func Croc_PollEvent(buffer *C.char, capacity C.int32_t, required *C.int32_t) (result C.int32_t) {
	defer recoverAPI(nil, 0, &result)
	if required == nil {
		engine.SetLastError("required output is required")
		return C.int32_t(engine.CodeInvalidArgument)
	}
	*required = 0
	written := C.int32_t(0)
	write := func(data []byte) bool {
		needed := C.int32_t(len(data) + 1)
		if buffer == nil || capacity <= 0 || capacity < needed {
			*required = needed
			return false
		}
		dst := unsafe.Slice((*byte)(unsafe.Pointer(buffer)), int(capacity))
		copy(dst, data)
		dst[len(data)] = 0
		written = C.int32_t(len(data))
		return true
	}
	code, requiredLength, err := globalEngine.PollEvent(write)
	if err != nil {
		engine.SetLastError(err.Error())
		return C.int32_t(code)
	}
	if code == engine.CodeEventAvailable {
		*required = written
	} else if code == engine.CodeBufferTooSmall {
		*required = C.int32_t(requiredLength)
	}
	return C.int32_t(code)
}

//export Croc_RespondToPrompt
func Croc_RespondToPrompt(taskID C.uint64_t, promptID *C.char, decision C.int32_t) (result C.int32_t) {
	defer recoverAPI(nil, 0, &result)
	if promptID == nil {
		engine.SetLastError("prompt id is required")
		return C.int32_t(engine.CodeInvalidArgument)
	}
	code, err := globalEngine.RespondToPrompt(uint64(taskID), C.GoString(promptID), int32(decision))
	if err != nil {
		engine.SetLastError(err.Error())
	}
	return C.int32_t(code)
}

//export Croc_ReleaseTask
func Croc_ReleaseTask(taskID C.uint64_t) (result C.int32_t) {
	defer recoverAPI(nil, 0, &result)
	code, err := globalEngine.ReleaseTask(uint64(taskID))
	if err != nil {
		engine.SetLastError(err.Error())
	}
	return C.int32_t(code)
}

//export Croc_GetLastError
func Croc_GetLastError(buffer *C.char, capacity C.int32_t, required *C.int32_t) (result C.int32_t) {
	defer recoverAPI(nil, 0, &result)
	if required == nil {
		engine.SetLastError("required output is required")
		return C.int32_t(engine.CodeInvalidArgument)
	}
	return C.int32_t(copyJSON([]byte(engine.LastError()), buffer, capacity, required))
}

//export Croc_GetTaskCount
func Croc_GetTaskCount() (result C.int32_t) {
	defer recoverAPI(nil, 0, &result)
	return C.int32_t(globalEngine.TaskCount())
}

//export Croc_GetActiveTaskCount
func Croc_GetActiveTaskCount() (result C.int32_t) {
	defer recoverAPI(nil, 0, &result)
	return C.int32_t(globalEngine.ActiveTaskCount())
}

//export Croc_GetDroppedEventCount
func Croc_GetDroppedEventCount() (result C.uint64_t) {
	defer func() {
		if recovered := recover(); recovered != nil {
			engine.SetLastError(fmt.Sprintf("panic in croc engine API: %v", recovered))
			result = 0
		}
	}()
	return C.uint64_t(globalEngine.DroppedEventCount())
}
