# Croc-lib

简体中文 · [English](README.en.md)

Croc-lib 将 [croc](https://github.com/schollz/croc) 的加密文件传输功能封装成动态库。C/C++ 程序可以通过 C 接口收发文件、读取进度，并在自己的界面中处理接收或覆盖确认。想直接在桌面上传文件，请使用 [Croc-DawnUI](https://github.com/rayccr/Croc-DawnUI)。

## 在 Windows 上接入

1. 安装 Go 1.27.1 或更高版本、MinGW-w64 GCC 和 Visual Studio 2022 C++ 工具。在仓库根目录的 PowerShell 中运行 `.\scripts\build-windows.ps1 -SkipTests`。
2. 构建产物位于 `dist/windows-x64/`。在 C/C++ 工程中包含 `croc_engine_api.h`，用 MSVC 链接 `croc_engine.lib`，并把 `croc_engine.dll` 放在可执行文件旁。
3. 可以先用 [C++ 加载示例](examples/cpp/version.cpp) 读取引擎版本。要开始收发文件，请按 [API 文档](docs/croc-engine-api.md) 初始化引擎、创建任务并读取事件。

在 Linux 上构建动态库，请参阅 [Ubuntu 构建说明](docs/UBUNTU.md)。

## 项目结构

| 目录 | 内容 |
| --- | --- |
| [bridge/](bridge/) | C 接口、任务管理和传输事件 |
| [croc-go/](croc-go/) | 本库使用的 croc 传输核心 |
| [examples/cpp/](examples/cpp/) | C++ 调用示例 |
| [scripts/](scripts/) | 各平台的构建脚本 |

[MIT 许可证](LICENSE) · [上游项目说明](UPSTREAM.md) · [第三方组件](THIRD_PARTY_NOTICES.md)
