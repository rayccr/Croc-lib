/**
****************************************************************
* @file    croc_engine_api.h
* @brief   Croc Engine 跨平台 C ABI（Croc 11.6.0 传输核心）
* @author  Croc-DawnUI
* @date    2026-11-01
* @note    V1.0.0       2026-11-01    1. 创建统一 Windows/Linux/Android C ABI
****************************************************************
*/
#ifndef CROC_ENGINE_API_H
#define CROC_ENGINE_API_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

#define CROC_ENGINE_API_VERSION 1
#define CROC_ENGINE_JSON_SCHEMA_VERSION 1

#if defined(_WIN32)
#  if defined(CROC_ENGINE_BUILD)
#    define CROC_ENGINE_API __declspec(dllexport)
#  else
#    define CROC_ENGINE_API
#  endif
#else
#  define CROC_ENGINE_API __attribute__((visibility("default")))
#endif

/* 所有函数返回 int32_t；0 表示成功，负数为错误码。
 * Croc_PollEvent 的返回码例外：1=取到事件，0=当前无事件，负数为错误。 */
typedef enum CrocResult
{
    CROC_OK = 0,
    CROC_NO_EVENT = 0,
    CROC_EVENT_AVAILABLE = 1,
    CROC_ERR_INVALID_ARGUMENT = -1,
    CROC_ERR_NOT_INITIALIZED = -2,
    CROC_ERR_ALREADY_INITIALIZED = -3,
    CROC_ERR_TASK_NOT_FOUND = -4,
    CROC_ERR_BUFFER_TOO_SMALL = -5,
    CROC_ERR_INVALID_JSON = -6,
    CROC_ERR_INVALID_CONFIG = -7,
    CROC_ERR_TASK_BUSY = -8,
    CROC_ERR_PROMPT_NOT_FOUND = -9,
    CROC_ERR_INVALID_DECISION = -10,
    CROC_ERR_SHUTTING_DOWN = -11,
    CROC_ERR_INTERNAL = -12,
    CROC_ERR_TIMEOUT = -13
} CrocResult;

/* 提示回答。accept/reject 用于接收确认和发送确认；
 * overwrite/skip/resume 用于已存在文件；resume 等价于接受覆盖，
 * Croc 会根据已有分块自动续传。 */
typedef enum CrocPromptDecision
{
    CROC_PROMPT_REJECT = 0,
    CROC_PROMPT_ACCEPT = 1,
    CROC_PROMPT_OVERWRITE = 2,
    CROC_PROMPT_SKIP = 3,
    CROC_PROMPT_RESUME = 4
} CrocPromptDecision;

/**
 * @brief 返回 ABI 版本字符串（静态存储，调用方不得释放）。
 */
CROC_ENGINE_API char* Croc_GetApiVersion(void);

/**
 * @brief 返回 Croc 核心版本字符串（静态存储，调用方不得释放）。
 */
CROC_ENGINE_API char* Croc_GetEngineVersion(void);

/**
 * @brief 初始化引擎。
 * @return CROC_OK；重复调用返回 CROC_ERR_ALREADY_INITIALIZED。
 * @note 引擎关闭后可重新初始化；任务 ID 在进程生命周期内不复用。
 */
CROC_ENGINE_API int32_t Croc_Initialize(void);

/**
 * @brief 取消所有活动任务并等待其退出。
 * @return CROC_OK；等待超时返回 CROC_ERR_TIMEOUT，此时引擎保持可用。
 * @note 不会调用 os.Exit()，不会终止宿主 Qt 进程。
 */
CROC_ENGINE_API int32_t Croc_Shutdown(void);

/**
 * @brief 异步启动发送任务。
 * @param _pszConfigJson  UTF-8 JSON；未知字段会被拒绝。
 * @param _pnTaskId       成功时写入任务句柄；失败时不修改。
 * @param _pszError       可选错误缓冲区，可为 NULL；失败时写入以 NUL 结尾的 UTF-8。
 * @param _nErrorCapacity 错误缓冲区字节数。
 * @return CROC_OK 或错误码。
 */
CROC_ENGINE_API int32_t Croc_StartSend(char* _pszConfigJson,
                                       uint64_t* _pnTaskId,
                                       char* _pszError,
                                       int32_t _nErrorCapacity);

/**
 * @brief 异步启动接收任务。参数语义同 Croc_StartSend。
 */
CROC_ENGINE_API int32_t Croc_StartReceive(char* _pszConfigJson,
                                          uint64_t* _pnTaskId,
                                          char* _pszError,
                                          int32_t _nErrorCapacity);

/**
 * @brief 请求取消指定任务。只影响该任务，不阻塞调用线程。
 * @note 重复取消、对已结束任务取消都返回 CROC_OK；非法任务返回错误。
 */
CROC_ENGINE_API int32_t Croc_CancelTask(uint64_t _nTaskId);

/**
 * @brief 查询任务是否已结束。
 * @param _pnFinished 写入 1（已结束）或 0（运行中）。
 */
CROC_ENGINE_API int32_t Croc_IsTaskFinished(uint64_t _nTaskId, int32_t* _pnFinished);

/**
 * @brief 获取任务完整 JSON 快照。
 * @param _pszBuffer    输出缓冲区，可为 NULL（此时 _nCapacity 应为 0）。
 * @param _nCapacity    缓冲区字节数。
 * @param _pnRequired   成功时写入实际字节数（不含 NUL）；缓冲区不足时写入所需字节数（含 NUL）。
 * @return CROC_OK；缓冲区不足返回 CROC_ERR_BUFFER_TOO_SMALL 且不破坏缓冲区。
 * @note 缓冲区充足时自动追加 NUL，输出为 UTF-8。
 */
CROC_ENGINE_API int32_t Croc_GetTaskSnapshot(uint64_t _nTaskId,
                                             char* _pszBuffer,
                                             int32_t _nCapacity,
                                             int32_t* _pnRequired);

/**
 * @brief 非阻塞取出一条结构化 JSON 事件。
 * @param _pszBuffer    输出缓冲区，可为 NULL（此时 _nCapacity 应为 0）。
 * @param _nCapacity    缓冲区字节数。
 * @param _pnRequired   无事件时为 0；取到事件时为实际字节数（不含 NUL）；
 *                      缓冲区不足时为所需字节数（含 NUL）。
 * @return CROC_EVENT_AVAILABLE、CROC_NO_EVENT 或错误码。
 * @note 缓冲区不足时事件保留在队列中，可用更大缓冲区重试。
 */
CROC_ENGINE_API int32_t Croc_PollEvent(char* _pszBuffer,
                                       int32_t _nCapacity,
                                       int32_t* _pnRequired);

/**
 * @brief 回答 Croc 发起的交互提示。
 * @param _pszPromptId 事件中的 prompt_id，UTF-8，以 NUL 结尾。
 * @param _nDecision   CrocPromptDecision 值。
 * @note 同一 prompt_id 只能成功回答一次；非法/过期提示返回错误。
 */
CROC_ENGINE_API int32_t Croc_RespondToPrompt(uint64_t _nTaskId,
                                             char* _pszPromptId,
                                             int32_t _nDecision);

/**
 * @brief 释放已结束任务。任务必须已结束；重复释放返回 CROC_ERR_TASK_NOT_FOUND。
 */
CROC_ENGINE_API int32_t Croc_ReleaseTask(uint64_t _nTaskId);

/**
 * @brief 读取最近一次 API 调用写入的错误文本（UTF-8）。
 * @note 该接口只作诊断辅助；错误码仍是权威结果。缓冲区语义同快照。
 */
CROC_ENGINE_API int32_t Croc_GetLastError(char* _pszBuffer,
                                          int32_t _nCapacity,
                                          int32_t* _pnRequired);

/**
 * @brief 返回当前未释放任务数（含已结束未释放）。
 */
CROC_ENGINE_API int32_t Croc_GetTaskCount(void);

/**
 * @brief 返回仍在运行的任务数。
 */
CROC_ENGINE_API int32_t Croc_GetActiveTaskCount(void);

/**
 * @brief 返回因队列容量限制被丢弃的事件数（诊断用）。
 */
CROC_ENGINE_API uint64_t Croc_GetDroppedEventCount(void);

#ifdef __cplusplus
}
#endif

#endif /* CROC_ENGINE_API_H */
