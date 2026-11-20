# Croc-lib

[English](README.md) | [简体中文](README.zh-CN.md)

基于 [schollz/croc](https://github.com/schollz/croc) 改造的Go文件传输库，通过C ABI供C/C++等宿主调用，由Ray维护，供[Croc-DawnUI](https://github.com/rayccr/Croc-DawnUI)使用。

基于Croc v11.6.0，提交f707beef66921431eea567887eb94ab7efbfd038。croc-go/包含带库化补丁的源码快照，版本和修改范围见[UPSTREAM.md](UPSTREAM.md)。


## 构建

需要Windows x64、Go 1.27.x（已验证1.27.3）、MinGW-w64 GCC和Visual Studio 2022 C++工具。源码快照已随仓库提供，无需初始化嵌套Git仓库。根据本机安装位置调整参数。

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build-windows.ps1 -GoExe C:/Go/bin/go.exe -CC C:/msys64/mingw64/bin/gcc.exe -SkipTests
```

产物在dist/windows-x64/。运行完整本机验收时去掉-SkipTests，并传入-OriginalCroc <croc.exe路径>。CLI用于测试中继和互通验证。构建保留调用者的Go模块校验设置。

## Linux 构建

参见 [Ubuntu 构建说明](docs/UBUNTU.md)。Linux 双端收发仍待实机验证。

## C++调用

接口在bridge/include/croc_engine_api.h。可动态加载DLL并查找函数，或使用MSVC导入库链接。启动接口接收UTF-8 JSON并返回任务编号，宿主查询事件/快照、回答确认、取消指定任务并释放已结束任务。输出写入调用者提供的缓冲区。详见[API文档](docs/croc-engine-api.md)、[C++示例](examples/cpp/version.cpp)和tests/engine-api/。

目录中bridge/cmd提供cgo导出，bridge/engine负责任务和事件，croc-go保留上游补丁源码，scripts负责Windows构建，tests包含独立C++验证。

## 当前状态与限制

Windows x64 已进行本机验证。本机验证覆盖文件收发、接收确认、取消、结构化进度、队列调度和中断记录恢复。跨电脑局域网发现、弱网、休眠、断电和恶意文件系统竞态仍需更多验证。全零合成文件的同名覆盖可能重复请求数据，属于已知问题。可靠暂停尚未开放。重启后发送方恢复并重新分享就绪口令，接收方输入当前有效口令，再由引擎匹配可复用内容。部分文件可能使用正式文件名和预分配大小，成功状态以最终确认结果为准。

所选引擎配置可能使用公共中继。对端地址只作参考，不代表身份认证。

## 许可证

新增代码使用[MIT](LICENSE)。上游Croc版权及许可在croc-go/LICENSE和licenses/Croc-MIT.txt保留。依赖保留各自许可，见[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。贡献、安全反馈及版本记录分别见CONTRIBUTING.md、SECURITY.md和CHANGELOG.md。
