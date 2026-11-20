# Croc Engine C ABI 文档

更新：2026-11-01。对应 Croc 11.6.0 / 固定 commit `f707beef66921431eea567887eb94ab7efbfd038`。
本轮实际验收平台为 Windows x64；Linux 与 Android 按用户最新指令暂不纳入本轮验收。

## 1. 目标与架构

Croc Engine 保留 Croc 原始 Go 传输核心（P2P/中继、PAKE、加密、文件 I/O、哈希、断点续传），在核心外层增加：

```text
Qt / C++ / 其他 C/C++ 程序
        │  Croc_* C ABI（UTF-8 JSON + 输出缓冲区）
        ▼
CGO Bridge（bridge/cmd/croc_engine）
        │
        ▼
Go Task Manager（bridge/engine）
        │  task id / context / cancel / prompt / event queue
        ▼
Croc Core（croc-go/src/croc，最小库化补丁）
        │
        ├── 发送/接收 / P2P / 中继 / 加密 / 文件 I/O
        ├── ProgressObserver：真实字节、阶段、生命周期
        └── 任务内 PromptHandler：取代 stdin 交互
```

关键原则：

- 不重写网络协议，不改变 Croc 报文格式。
- 不通过解析终端进度条生成状态。
- 不把 Go 指针、Go string/slice/map/channel 暴露给 C。
- 每个任务独立 Client、Context、取消、事件和提示；不使用全局工作目录。
- Windows 首版关闭 Croc 的 bundle 和 staged-transport 可选优化，走经过验证的逐文件中继路径；原版 CLI 会自动回退，协议互通已实测。

## 2. 源码与产物

| 路径 | 说明 |
| --- | --- |
| `PROJECT/Croc-lib/croc-go/` | Croc v11.6.0 源码；库化补丁见 `git -C croc-go diff` |
| `PROJECT/Croc-lib/croc-go/src/croc/library.go` | 库化回调、终端隔离、接收目录、清单/文件名适配 |
| `PROJECT/Croc-lib/bridge/engine/` | 任务、配置、事件、验证、提示 |
| `PROJECT/Croc-lib/bridge/cmd/croc_engine/main.go` | CGO 导出 |
| `PROJECT/Croc-lib/bridge/include/croc_engine_api.h` | 唯一公开头文件 |
| `PROJECT/Croc-lib/dist/windows-x64/croc_engine.dll` | Windows x64 DLL |
| `PROJECT/Croc-lib/dist/windows-x64/croc_engine.lib` | MSVC 导入库 |
| `PROJECT/Croc-lib/dist/windows-x64/croc_engine_api.h` | 与 DLL 配套的公开头文件 |
| `PROJECT/Croc-lib/tests/engine-api/` | 独立 C++20 测试 |

公开头文件中的函数签名与 Go 生成的 `croc_engine.h` 一致（参数名可以不同，类型、调用约定和导出名一致）。

## 3. 构建与测试（Windows）

前置：Go 1.27.3、MinGW-w64 gcc、VS 2022 C++ 工具链、本机 `engine/croc.exe`（只用于启动测试 relay）。

```powershell
cd D:\Croc-DawnUI\PROJECT\Croc-lib
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\build-windows.ps1
```

脚本会：

1. 检查 Go 1.27.x、gcc、MSVC。
2. 校验 Croc commit 必须为 `f707beef...`、tag 为 `v11.6.0`。
3. `go mod download`，构建 `croc_engine.dll`。
4. 生成 `.def` 和 MSVC `.lib`，检查 16 个导出符号。
5. 检查 DLL 依赖只能是系统库，不能依赖 MinGW 运行库。
6. 编译独立 C++20 测试。
7. 启动本机 relay，运行 C++ API 测试。
8. 用固定源码构建 v11.6.0 CLI，双向验证：CLI→DLL、DLL→CLI，比较 SHA-256。
9. 附加验证用户原有 `engine/croc.exe` 作为发送端。

仅构建：加 `-SkipTests`。测试脚本可单独运行：

```powershell
.\scripts\test-windows.ps1 -DllPath .\dist\windows-x64\croc_engine.dll `
  -TestExe .\build\final2\CrocEngineApiTests.exe -RelayPort 9309
```

### 3.1 C ABI 总览

`croc_engine_api.h` 中所有函数均为 `extern "C"`，只使用 `int32_t`、`uint64_t`、`char*`。没有 Qt、QObject、QString、STL 或 Go 类型。

### 3.2 函数表

| 函数 | 说明 |
| --- | --- |
| `Croc_GetApiVersion()` | 静态字符串 `"1.0.0"`；不要释放或修改。 |
| `Croc_GetEngineVersion()` | 静态字符串 `"11.6.0"`；不要释放或修改。 |
| `Croc_Initialize()` | 初始化；重复调用返回 `CROC_ERR_ALREADY_INITIALIZED`。 |
| `Croc_Shutdown()` | 取消活动任务并等待（上限 20s）；不调用 `os.Exit()`。超时返回 `CROC_ERR_TIMEOUT`，引擎仍可用。 |
| `Croc_StartSend(json, out_id, err, errcap)` | 异步发送；返回唯一 `uint64_t`。配置错误同步返回。 |
| `Croc_StartReceive(json, out_id, err, errcap)` | 异步接收。 |
| `Croc_CancelTask(id)` | 取消指定任务，不阻塞；重复取消安全。 |
| `Croc_IsTaskFinished(id, out)` | 1=已结束，0=运行中。 |
| `Croc_GetTaskSnapshot(id, buf, cap, required)` | 完整 JSON 快照。缓冲区不足返回 `CROC_ERR_BUFFER_TOO_SMALL`，不破坏缓冲区。 |
| `Croc_PollEvent(buf, cap, required)` | 1=有事件，0=无事件，负数为错误；缓冲区不足时事件保留。 |
| `Croc_RespondToPrompt(id, prompt_id, decision)` | 回答提示；同一 prompt 只能成功一次。 |
| `Croc_ReleaseTask(id)` | 只释放已结束任务；运行中返回 `CROC_ERR_TASK_BUSY`。 |
| `Croc_GetLastError(buf, cap, required)` | 最近一次 API 错误文本（诊断辅助）。 |
| `Croc_GetTaskCount()` | 未释放任务数（含已结束未释放）。 |
| `Croc_GetActiveTaskCount()` | 运行中任务数。 |
| `Croc_GetDroppedEventCount()` | 因队列上限被丢弃的事件数。 |

错误码：`CROC_ERR_INVALID_ARGUMENT=-1`、`NOT_INITIALIZED=-2`、`ALREADY_INITIALIZED=-3`、`TASK_NOT_FOUND=-4`、`BUFFER_TOO_SMALL=-5`、`INVALID_JSON=-6`、`INVALID_CONFIG=-7`、`TASK_BUSY=-8`、`PROMPT_NOT_FOUND=-9`、`INVALID_DECISION=-10`、`SHUTTING_DOWN=-11`、`INTERNAL=-12`、`TIMEOUT=-13`。

### 3.3 缓冲区规则

- 所有输出 JSON 为 UTF-8，成功时以 NUL 结尾。
- `cap` 是缓冲区字节数（含 NUL）。
- 快照/事件成功时 `required` 为**不含 NUL**的字节数；缓冲区不足时 `required` 为**含 NUL**的所需字节数。
- 缓冲区为 NULL 或 `cap=0` 返回 `BUFFER_TOO_SMALL`，调用方按 `required` 扩容后重试。
- 事件在缓冲区不足时不会出队。
- 版本字符串是 Go 进程生命周期内的静态分配，调用方不得 `free`。

### 3.4 线程安全

- 所有 `Croc_*` 函数可以从任意线程调用；引擎内部用互斥锁保护任务表、事件队列和任务快照。
- 同一任务 ID 的取消/释放/快照/提示回答可以并发调用；释放只允许在结束后。
- `Croc_PollEvent` 是全局 FIFO；多个线程同时轮询时每个事件只会被一个调用方取走。
- `Croc_GetLastError` 是进程级“最近错误”，不保证与调用线程一一对应。
- 不建议在 DLL 导出函数内做长时间阻塞；`Croc_Shutdown` 是例外，它可能等待活动任务退出。

## 4. 配置 JSON

两个入口都使用严格 JSON：未知字段、错误类型、非法路径直接返回 `CROC_ERR_INVALID_CONFIG`，不会静默忽略。`files` 和 `save_path` 必须是绝对路径。

### 4.1 发送 `Croc_StartSend`

```json
{
  "code": "custom-code",
  "files": ["D:\\data\\a.bin", "D:\\data\\folder"],
  "zip_folder": false,
  "relay": "127.0.0.1:9009",
  "relay6": "",
  "relay_password": "pass123",
  "only_local": false,
  "disable_local": true,
  "local_relay_ports": [],
  "no_compress": false,
  "no_multiplexing": false,
  "hash_algorithm": "xxhash",
  "curve": "p256",
  "throttle_upload": "",
  "transport": "auto",
  "multicast_address": "239.255.255.250",
  "exclude": [],
  "exclude_file": [],
  "ask": false,
  "prompt_timeout_seconds": 0
}
```

| 字段 | 说明 |
| --- | --- |
| `code` | 必填，至少 6 字节；由调用方生成。DLL 不生成口令，避免把口令写日志。 |
| `files` | 必填，非空绝对路径数组；文件或目录；不支持通配符。 |
| `zip_folder` | 目录打包后发送；接收端会解压；该模式校验状态为 unavailable。 |
| `relay` / `relay6` | Croc 中继；空则默认公共中继。自定义 relay 时 relay6 会被清空。 |
| `relay_password` | 默认 `pass123`。 |
| `only_local` | 仅局域网；隐含 `disable_local=false`，要求 `local_relay_ports`。 |
| `disable_local` | 默认 `true`（库模式避免多任务抢占固定本地 relay 端口）。设为 false 时必须提供 `local_relay_ports`。 |
| `local_relay_ports` | 本地 relay 端口数组，至少 2 个，且不能重复。 |
| `no_compress` | 关闭压缩。 |
| `no_multiplexing` | 关闭多路复用。 |
| `hash_algorithm` | `xxhash`（默认）、`md5`、`highway`、`imohash`。 |
| `curve` | `p256`（默认）、`p384`、`p521`、`siec`、`ed25519`。 |
| `throttle_upload` | 例如 `500k`、`4m`；Croc 的限速 burst 等于该值，小文件可能瞬时完成。 |
| `transport` | `auto`（默认）、`relay`、`derp`。 |
| `multicast_address` | 局域网发现地址。 |
| `exclude` | 大小写不敏感的子串排除，作用于相对路径。 |
| `exclude_file` | 精确相对路径排除。 |
| `ask` | 发送端在对方请求后要求确认；接收端会收到 `receive_accept` 提示。 |
| `prompt_timeout_seconds` | 0–3600；0 表示等待调用方取消。 |

### 4.2 接收 `Croc_StartReceive`

```json
{
  "code": "custom-code",
  "save_path": "D:\\downloads",
  "auto_accept": true,
  "overwrite": false,
  "rename": false,
  "relay": "127.0.0.1:9009",
  "relay6": "",
  "relay_password": "pass123",
  "only_local": false,
  "disable_local": true,
  "local_relay_ports": [],
  "no_compress": false,
  "no_multiplexing": false,
  "curve": "p256",
  "transport": "auto",
  "multicast_address": "239.255.255.250",
  "ip": "",
  "prompt_timeout_seconds": 0
}
```

| 字段 | 说明 |
| --- | --- |
| `save_path` | 必填，绝对目录；启动前探测存在、目录、可写。 |
| `auto_accept` | 默认 `true`；false 时发送文件清单后发出 `receive_accept` 提示。 |
| `overwrite` | 同名文件直接覆盖/续传；false 时发 `overwrite` 提示。 |
| `rename` | 同名时自动改名，不发覆盖提示。 |
| `ip` | 指定发送端中继/直连地址；Croc 原字段。 |
| `transport` | 接收端必须是 `auto`；发送端决定数据通道。 |

接收端不接受 `hash_algorithm`：哈希算法由发送端在文件清单中协商，接收端使用发送端算法进行最终校验。

## 5. 任务生命周期

```text
Croc_StartSend/StartReceive
        │ 同步校验配置/路径/口令
        ▼
task id（uint64） ── 异步 preparing/connecting/...
        │
        ├── Croc 传输
        ├── ProgressObserver 真实字节/阶段
        ├── PromptHandler（如需）── Croc_RespondToPrompt
        └── Receive 成功后的逐文件最终校验
        ▼
terminal 事件（completed / failed / cancelled）
        │
        ├── Croc_GetTaskSnapshot（仍可读）
        ├── Croc_IsTaskFinished == 1
        └── Croc_ReleaseTask（之后 id 失效）
```

规则：

- 任务 ID 在进程生命周期内单调递增，不复用。
- 取消是**请求**；需轮询到终态或 `Croc_IsTaskFinished=1` 后才算引擎真正退出。重复取消返回 0。
- 完成与释放分离；释放运行中任务返回 `CROC_ERR_TASK_BUSY`。
- `Croc_Shutdown` 会取消活动任务并等待，之后清空任务表和事件队列；可重新 `Croc_Initialize`。
- 释放后再次快照/释放返回 `CROC_ERR_TASK_NOT_FOUND`。
- 终态事件保证是该任务最后一条事件；事件顺序由 `sequence` 递增。

## 6. 状态、阶段与校验

### 6.1 阶段

| 阶段 | 含义 |
| --- | --- |
| `preparing` | 扫描源文件/准备 |
| `connecting` | 连接中继/启动 |
| `waiting_peer` | 已连上中继，等待对端或清单 |
| `negotiating` | PAKE/会话协商 |
| `transferring` | 真实数据字节在流动 |
| `verifying` | 接收端校验阶段 |
| `awaiting_confirmation` | 等待用户确认（接收确认/覆盖/发送确认） |
| `completed` | 成功终态 |
| `cancelled` | 取消终态 |
| `failed` | 失败终态 |

`saving` 没有单独伪造：Croc 逐文件直接写入，没有可独立计量的“保存”阶段；bundle 发布阶段在库模式被关闭。传输到 100% 不等于任务完成，发送端会进入 `awaiting_confirmation`，接收端随后进入 `verifying`。

### 6.2 进度语义

- `totals.bytes_done`：逻辑已完成字节（含断点续传复用）。
- `totals.session_bytes`：本次会话真实网络字节，**不含复用字节**。
- `transfer.speed_bps`：基于 `session_bytes` 增量，150ms 采样，EWMA 平滑；超过约 2s 无新字节会衰减到 0。
- `transfer.eta_seconds`：仅在 `transferring` 且有正速度、正剩余字节时给出，否则为 `null`。
- `transfer.percent`：`bytes_done / bytes_total * 100`；总字节为 0 时为 `null`。
- 空文件按 0 字节计入清单；全部为 0 时 `percent=null`，最终以任务状态和文件状态判断完成。
- 跳过/未变化文件的字节不计入 `bytes_done`，因此完成任务的百分比可能小于 100；用 `files_skipped`/文件状态区分。
- `verification.bytes_done/bytes_total`：最终逐文件哈希实际读取字节；采样哈希（imohash/imohash-v2）无法准确计量时这两个字段为 `null`，只报告阶段和结果。
- `zip_folder` 解压、符号链接、无法读取的文件标记为 unavailable，不伪造百分比。

### 6.3 最终校验

接收成功后，引擎对每个普通文件重新读取并计算发送端声明的哈希（默认 xxhash；也支持 md5/highway/imohash），与文件清单中的哈希比较：

- 一致：`verification.status=passed`，任务 `completed`。
- 不一致/读取失败：`verification.status=failed`，任务 `failed`，错误码 `verification_failed`。
- 用户跳过：`files_skipped` 增加，`verification.status=skipped`。
- 符号链接/zip 包：`unavailable`。
- 发送端：`verification.status=not_applicable`；Croc 在发送前已计算源文件哈希。

## 7. 事件 JSON

所有事件都含：

```json
{
  "schema_version": 1,
  "sequence": 42,
  "event": "progress",
  "task_id": 3,
  "timestamp": "2026-11-01T13:00:00.0000000Z"
}
```

### 7.1 `task_started`

```json
{"event":"task_started","direction":"send","state":"running","phase":"preparing","started_at":"..."}
```

### 7.2 `files`

```json
{
  "event":"files",
  "direction":"receive",
  "files":[
    {"index":0,"name":"a.bin","path":"D:\\recv\\a.bin","size":1048576,"bytes_done":0,"session_bytes":0,"reused_bytes":0,"status":"pending"}
  ],
  "total_folders": 0,
  "totals": {"files":1,"files_completed":0,"files_skipped":0,"files_failed":0,"folders":0,"bytes_total":1048576,"bytes_done":0,"session_bytes":0}
}
```

### 7.3 `phase`

```json
{"event":"phase","phase":"transferring","previous":"waiting_peer","state":"running","totals":{...},"file":{...},"transfer":{...},"verification":{...}}
```

### 7.4 `progress`

```json
{
  "event": "progress",
  "direction": "receive",
  "state": "running",
  "phase": "verifying",
  "totals": {"files":3,"files_completed":1,"files_skipped":0,"files_failed":0,"folders":0,"bytes_total":314572800,"bytes_done":314572800,"session_bytes":104857600},
  "file": {"index":2,"name":"example.bin","path":"D:\\recv\\example.bin","size":104857600,"bytes_done":52428800,"session_bytes":52428800,"reused_bytes":0,"status":"transferring"},
  "transfer": {"bytes_done":314572800,"bytes_total":314572800,"session_bytes":104857600,"speed_bps":12582912,"eta_seconds":20.8,"percent":100},
  "verification": {"status":"running","bytes_done":52428800,"bytes_total":314572800,"speed_bps":104857600,"files_total":3,"files_done":1,"files_failed":0}
}
```

进度事件在队列中按任务合并：同一任务最多一条待取进度事件；关键事件（task_started/files/phase/prompt/terminal）不会被进度事件挤掉。`Croc_GetDroppedEventCount` 可发现异常丢弃。

### 7.5 `prompt`

```json
{
  "event":"prompt",
  "prompt_id":"3-1",
  "prompt_kind":"overwrite",
  "file":{"index":0,"name":"report.bin","destination":"D:\\recv\\report.bin"},
  "files_count":1,
  "bytes_total":10485760,
  "peer_machine_id":"...",
  "resume_percent":42.5,
  "default":"reject",
  "totals":{...},
  "transfer":{...}
}
```

`prompt_kind`：

- `receive_accept`：接收清单确认；决定 `CROC_PROMPT_ACCEPT` / `CROC_PROMPT_REJECT`。
- `overwrite`：覆盖/续传；`CROC_PROMPT_OVERWRITE`、`CROC_PROMPT_SKIP`、`CROC_PROMPT_RESUME`。
- `folder_overwrite`：非空目录覆盖；`CROC_PROMPT_OVERWRITE` / `CROC_PROMPT_REJECT`。
- `send_approval`：发送端 `--ask` 模式确认。

同一个 `prompt_id` 只能回答一次；重复回答返回 `CROC_ERR_PROMPT_NOT_FOUND`。如果 C++ 不读取事件，需要用户确认的传输会停在 `awaiting_confirmation`；可通过 `Croc_CancelTask` 或 `prompt_timeout_seconds` 结束。

### 7.6 `terminal`

```json
{
  "event":"terminal",
  "state":"completed",
  "phase":"completed",
  "started_at":"...",
  "finished_at":"...",
  "error":null,
  "error_code":null,
  "totals":{...},
  "transfer":{...},
  "verification":{"status":"passed","bytes_done":...,"bytes_total":...,"files_total":1,"files_done":1,"files_failed":0}
}
```

失败时 `error` 为 UTF-8 文本，`error_code` 为 `config_error`、`network_error`、`authentication_failed`、`verification_failed`、`cancelled`、`engine_error` 等。

## 8. 快照 JSON

`Croc_GetTaskSnapshot` 返回与事件相同语义的完整对象，并额外包含 `files` 数组：

```json
{
  "schema_version": 1,
  "task_id": 3,
  "direction": "receive",
  "state": "completed",
  "phase": "completed",
  "started_at": "...",
  "finished_at": "...",
  "error": null,
  "error_code": null,
  "totals": {"files":3,"files_completed":2,"files_skipped":1,"files_failed":0,"folders":0,"bytes_total":314572800,"bytes_done":209715200,"session_bytes":209715200},
  "file": {"index":1,"name":"b.bin","path":"D:\\recv\\b.bin","size":104857600,"bytes_done":104857600,"session_bytes":104857600,"reused_bytes":0,"status":"verified"},
  "transfer": {"bytes_done":209715200,"bytes_total":314572800,"session_bytes":209715200,"speed_bps":null,"eta_seconds":null,"percent":66.6},
  "verification": {"status":"passed","bytes_done":209715200,"bytes_total":209715200,"speed_bps":null,"files_total":2,"files_done":2,"files_failed":0},
  "files": [...]
}
```

文件状态：`pending`、`transferring`、`completed`、`verified`、`verifying`、`skipped`、`unchanged`、`failed`、`unavailable`。

## 9. 断点续传与复用

接收端在发送清单到达后，会对已存在且大小相同的目标文件扫描零块，计算缺失区间，通过 `recipient-ready` 告知发送端只发送缺失块。

- 只复用 Croc 当前版本能确认的缺失块；不会把“已有文件”直接当完整文件。
- 复用字节计入 `bytes_done`/`reused_bytes`，不计入 `session_bytes`。
- 实测 4 MiB 文件、前 2 MiB 已存在且后 2 MiB 为零：`session_bytes=2097152`，最终 SHA-256 一致。
- 被用户跳过的文件不参与校验，`verification.status=skipped`。

## 10. 内存所有权

| 数据 | 分配者 | 释放者 | 规则 |
| --- | --- | --- | --- |
| 输出 JSON/事件 | Go 内部构建 | 调用方无需释放；Go 复制到调用方缓冲区 | 不返回 Go 指针 |
| 版本字符串 | Go 进程静态 C 字符串 | 不释放 | 只读；不要修改 |
| 配置 JSON、prompt_id | 调用方 | 调用方 | 调用期间有效；Go 立即复制 |
| 任务 ID | 引擎 | 调用方在结束后 `Croc_ReleaseTask` | 不重复使用 |
| DLL 句柄 | 调用方 LoadLibrary | 调用方 FreeLibrary | 卸载前先 `Croc_Shutdown` |

## 11. 部署

Windows：

```text
your-app.exe
croc_engine.dll          # 放 EXE 同目录或用绝对路径 LoadLibrary
croc_engine_api.h        # 编译期
croc_engine.lib          # 可选：静态导入链接；也可纯 LoadLibrary/GetProcAddress
LICENSE
THIRD_PARTY_NOTICES.md
```

DLL 只依赖 `KERNEL32.dll`、`msvcrt.dll`，不需要 MinGW 运行库。使用 MSVC 导入库时，头文件声明为普通 `extern "C"`；无需 `__declspec(dllimport)` 也能链接。

## 12. C++ 调用示例（轮询）

```cpp
#include "croc_engine_api.h"
#include <windows.h>
#include <cstdint>
#include <string>
#include <vector>

int main()
{
    HMODULE h = LoadLibraryW(L"croc_engine.dll");
    auto init = (decltype(&Croc_Initialize))GetProcAddress(h, "Croc_Initialize");
    auto start = (decltype(&Croc_StartSend))GetProcAddress(h, "Croc_StartSend");
    auto poll = (decltype(&Croc_PollEvent))GetProcAddress(h, "Croc_PollEvent");
    auto shutdown = (decltype(&Croc_Shutdown))GetProcAddress(h, "Croc_Shutdown");

    init();
    uint64_t taskId = 0;
    char error[2048] = {};
    std::string config =
        R"({"code":"my-code","files":["D:\\data\\a.bin"],)"
        R"("relay":"127.0.0.1:9009","disable_local":true,"no_compress":true})";
    if (start(config.data(), &taskId, error, sizeof(error)) != CROC_OK)
    {
        return 1;
    }
    for (;;)
    {
        std::vector<char> buffer(256 * 1024);
        int32_t required = 0;
        int32_t rc = poll(buffer.data(), (int32_t)buffer.size(), &required);
        if (rc == CROC_EVENT_AVAILABLE)
        {
            std::string event(buffer.data());
            // 解析 event/task_id/phase/transfer；或交给项目内 JSON 库。
        }
        else if (rc == CROC_ERR_BUFFER_TOO_SMALL)
        {
            // 按 required 扩容后重试；事件不会丢失。
        }
        else if (rc == CROC_NO_EVENT)
        {
            // 继续做 UI 刷新，不要忙等。
        }
        // 完成/取消后 Croc_ReleaseTask(taskId)
    }
    // shutdown();
}
```

完整可运行示例见 `PROJECT/Croc-lib/tests/engine-api/CrocEngineApiTests.cpp`。

## 13. 实测结果（2026-11-01，Windows x64）

| 项目 | 结果 | 说明 |
| --- | --- | --- |
| DLL 构建 | 通过 | 44,003,544 字节，SHA-256 `B43C7241605862D43AB5F36A3A7B83F1AAA9FBA5B8F47F0E5CEED9DBD3187E29`；16 个导出符号；依赖仅 KERNEL32/msvcrt |
| C++20 MSVC 加载/调用 | 通过 | 独立 LoadLibrary，不依赖 Qt |
| 单文件 16 MiB（4m 限速） | 通过 | 真实阶段、速度、ETA、收端校验；SHA-256 按字节比较一致 |
| 断点续传 | 通过 | 复用 2 MiB，`session_bytes` 仅 2 MiB，最终内容一致 |
| 多文件/文件夹/空文件 | 通过 | 3 文件 + 嵌套 + 空目录；逐文件 verified |
| 接收确认/覆盖/跳过 | 通过 | prompt ID 隔离，重复回答被拒绝 |
| 取消等待/取消传输中 | 通过 | 终态 cancelled；重复取消安全 |
| 多任务并发 | 通过 | 两组收发同时完成，目录隔离 |
| 错误 | 通过 | 非法路径/未知字段/非法任务/错误口令/连接失败 |
| 关闭/重初始化 | 通过 | 活动任务被取消，不退出宿主进程 |
| CLI 发送 → DLL 接收 | 通过 | 固定源码 v11.6.0 CLI；SHA-256 一致 |
| DLL 发送 → CLI 接收 | 通过 | 同上 |
| 用户原有 `engine/croc.exe` 发送 → DLL 接收 | 通过 | SHA-256 一致 |
| 用户原有 `engine/croc.exe` 接收 | 未通过（环境/二进制） | 该预编译二进制在本机 `os.Root.OpenFile` 创建文件返回 Access denied；不作为 DLL 验收结论 |
| Go core 全量测试 | 部分 | 除 Windows 符号链接权限、Unix 目录权限测试外，与库化相关测试通过 |

## 14. 已知限制与未完成项

1. **暂停/继续未实现**：Croc v11.6.0 没有可靠的暂停接口；本库不冻结线程冒充暂停。只支持取消与重新发起；部分文件续传由 Croc 缺失块机制完成。
2. **Linux / Android 本轮未验收**：按用户最新指示暂不处理。Android 做过一次 arm64-v8a 交叉编译检查，但未在设备运行，不作为通过。Linux 未构建。
3. **代理/高级连接**：没有暴露 SOCKS5/HTTP 代理、端口/通道等 CLI 高级参数；未知字段会被拒绝。`transport=derp/relay/auto` 可用。
4. **口令生成**：DLL 要求调用方提供 `code`，不自动生成。当前 Qt 已有口令生成逻辑。
5. **Android SAF**：`save_path` 只接受普通文件系统绝对路径；不接受 `content://` URI。后续需要文件访问抽象层。
6. **bundle/staged 优化关闭**：为避免 Windows bundle 发布探测 Access denied 和 staged 通道不确定性，库模式默认 `NoBundle=true`、`NoStagedTransport=true`；协议仍与 v11.6.0 兼容，CLI 自动回退。未做关闭后的吞吐性能基准。
7. **校验范围**：zip 解压、符号链接、采样哈希无法给出准确字节百分比；标记 unavailable/null。接收端会重新读取每个普通文件做最终哈希，因此大文件校验耗时与磁盘读速相关。
8. **事件队列上限**：默认 8192；进度按任务合并，关键事件优先保留。极端情况下仍可能丢弃并增加 `Croc_GetDroppedEventCount()`。
9. **未读事件**：任务正常运行不需要 C++ 读取进度事件；但 `auto_accept=false` 或覆盖确认会阻塞等待 `Croc_RespondToPrompt`。
10. **预编译 croc.exe 接收问题**：本机固定 `engine/croc.exe` 接收文件时报 `Access denied`（与其发送端和本 DLL 无关）；固定源码重新构建的 v11.6.0 CLI 双向通过。后续 Qt 切换时建议用源码构建的 CLI 或直接使用本 DLL，不覆盖原二进制。
11. **版本字符串**：`Croc_GetApiVersion`/`Croc_GetEngineVersion` 返回静态只读 C 字符串，不能释放。
12. **Qt 集成尚未进行**：本轮没有改 Qt UI/CrocAdapter；后续用 LoadLibrary + 轮询事件替换 QProcess 终端解析。

## 15. Qt 后续集成建议

1. 在 `CrocAdapter` 中加载 `croc_engine.dll`，缓存 16 个函数指针；不要每个任务 LoadLibrary。
2. 每个 UI 任务保存 `uint64_t taskId`；用 QTimer 50–100ms 调 `Croc_PollEvent`，分发 `progress/phase/prompt/terminal` 到现有 `SCrocSnapshot`。
3. 用 `Croc_GetTaskSnapshot` 做首次/断线后的完整刷新；事件只增量更新。
4. 接收确认/覆盖/跳过/续传把 `prompt_id` 存到任务上下文；用户选择后调 `Croc_RespondToPrompt`。
5. 取消先调 `Croc_CancelTask`，继续轮询到 `terminal`/`IsTaskFinished` 再释放。
6. 退出应用时先取消活动任务，再 `Croc_Shutdown`，最后 FreeLibrary；不要从 DLL 回调里直接销毁 QObject。
7. 发送端仍负责生成/展示口令；DLL 不记录完整口令。
8. 接收路径必须传绝对路径；后续 Android SAF 需要单独抽象。


## Windows Qt 五阶段接入补充（2026-11-04）

schema_version=1 快照新增两个可选字符串：`peer_address`（引擎取得的对端地址元数据，不能作为身份认证）与 `local_relay_port`（发送任务实际绑定的本地控制端口；仅局域网）。C ABI函数及传输协议不变。Qt保持界面UUID独立于C ABI数字任务句柄。

Qt在DLL模式发送配置 `ask=true` 以用 `send_approval` 门控实际发送槽；接收 `auto_accept=false`，通过 `receive_accept` 与 `overwrite` 提示做明确应答。引擎提示最长保留3600秒用于排队，界面可见确认超时60秒；排队门控并非已传输。整批transfer、单文件file、校验verification分别映射，null保持未知。保存/完成还需Qt核对落盘目标清单。

`only_local=true` 时从库内端口组启动，不使用公网中继；Qt提供9841/9842起始数组，库绑定并保留实际套接字。接收可选 `ip` 明确指定私网/链路本地/回环字面IP:端口，不接受公网或主机名；同进程同口令的仅库任务可以在内存定位本地端口，其他设备仍使用发现或显式地址。跨设备发现与防火墙需专项验证。

应用重连与任务记录位于Qt的 `AppControllerQueue.cpp`，并非C ABI承诺断线后原会话永久存活。Qt判断重连窗口与安全前置检查，Croc判断可复用字节并作最终内容校验。每次新尝试释放旧句柄，任务记录不保存口令/密码。
