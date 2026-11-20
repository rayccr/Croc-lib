# Croc-lib

[English](README.md) | [简体中文](README.zh-CN.md)

A Go/C ABI file-transfer library derived from [schollz/croc](https://github.com/schollz/croc), maintained by Ray and used by [Croc-DawnUI](https://github.com/rayccr/Croc-DawnUI).

Base: Croc v11.6.0, commit f707beef66921431eea567887eb94ab7efbfd038. The modified source snapshot is included in croc-go/. See [UPSTREAM.md](UPSTREAM.md) for provenance and integration changes.


## Build

Use Windows x64, Go 1.27.x (validated 1.27.3), MinGW-w64 GCC and Visual Studio 2022 C++ tools. A nested Git checkout is not required; the pinned modified snapshot is included. Adjust paths for your installation.

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build-windows.ps1 -GoExe C:/Go/bin/go.exe -CC C:/msys64/mingw64/bin/gcc.exe -SkipTests
```

Output: dist/windows-x64/. Omit -SkipTests and pass -OriginalCroc <path/to/croc.exe> to run the local relay/CLI interoperability suite. The CLI is a test dependency. Scripts preserve the caller's Go module checksum settings.

## Linux build

See [Ubuntu build instructions](docs/UBUNTU.md). Native Linux transfer validation is pending.

## C/C++ integration

Include bridge/include/croc_engine_api.h. Load the DLL using LoadLibrary/GetProcAddress or link through the MSVC import library. StartSend/StartReceive accept UTF-8 JSON and return a numeric task handle. Poll events and snapshots, answer prompts, cancel only the requested task, then release finished handles. Caller-owned buffers carry output; Go pointers and Go objects stay inside the engine.

The normal lifecycle is Initialize → StartSend/StartReceive → PollEvent/GetTaskSnapshot → RespondToPrompt as needed → ReleaseTask after completion → Shutdown. Treat successful task creation separately from successful file delivery. See the [API specification](docs/croc-engine-api.md), [C++ example](examples/cpp/version.cpp) and tests/engine-api/.

## Source map

| Directory | Responsibility |
| --- | --- |
| bridge/cmd/croc_engine | cgo exports |
| bridge/engine | Tasks, strict config, events, snapshots and verification |
| bridge/include | Public C ABI |
| croc-go | Modified upstream source snapshot |
| scripts | Windows build and acceptance helpers |
| tests/engine-api | C++ API tests |

## Status and limitations

Windows x64 is the validated platform. Local Windows testing covers file transfers, confirmation handling, cancellation, structured progress, queue scheduling and interrupted-task recovery. Cross-machine LAN discovery, poor networks, suspend/resume, power loss and hostile filesystem races still need broader validation. A synthetic all-zero same-name overwrite case can repeatedly request data and remains a known issue. Reliable pause/resume is not exposed. Task restoration reconnects peers; after restarting the app the sender shares a newly ready code and the receiver enters the current code. Partial files can have their final name and preallocated size; only the confirmed result determines completion.

Public relays may be used according to the selected engine configuration. Endpoint addresses are informational and do not establish identity.

## License

Library additions are [MIT](LICENSE). Croc's original MIT copyright is preserved in croc-go/LICENSE and licenses/Croc-MIT.txt. Third-party components keep their respective licenses; see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). See [CONTRIBUTING.md](CONTRIBUTING.md), [SECURITY.md](SECURITY.md) and [CHANGELOG.md](CHANGELOG.md).
