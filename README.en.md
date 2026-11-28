# Croc-lib

[简体中文](README.md) · English

Croc-lib packages [croc](https://github.com/schollz/croc) as a shared library for encrypted file transfers. C and C++ applications can send and receive files, track progress, and handle receive or overwrite prompts in their own interface. For the desktop app, see [Croc-DawnUI](https://github.com/rayccr/Croc-DawnUI).

## Integrate on Windows

1. Install Go 1.27.1 or later, MinGW-w64 GCC, and the Visual Studio 2022 C++ tools. From PowerShell in the repository root, run `.\scripts\build-windows.ps1 -SkipTests`.
2. Find the build outputs in `dist/windows-x64/`. Include `croc_engine_api.h` in your project, link against `croc_engine.lib` with MSVC, and place `croc_engine.dll` beside your executable.
3. Use the [C++ loading example](examples/cpp/version.cpp) to read the engine version. To transfer files, follow the [API guide](docs/croc-engine-api.md) to initialize the engine, start a task, and read its events.

For Linux, follow the [Ubuntu build instructions](docs/UBUNTU.md).

## Project structure

| Directory | Contents |
| --- | --- |
| [bridge/](bridge/) | C API, task management, and transfer events |
| [croc-go/](croc-go/) | The croc transfer core used by this library |
| [examples/cpp/](examples/cpp/) | C++ integration example |
| [scripts/](scripts/) | Platform build scripts |

[MIT license](LICENSE) · [Upstream notes](UPSTREAM.md) · [Third-party notices](THIRD_PARTY_NOTICES.md)
