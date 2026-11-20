# Contributing / 贡献



Open an issue describing the problem before a large change. Keep changes focused, preserve file-safety and protocol behavior, and document affected platforms and tests. Use synthetic files in regression tests. Submit contributions under the repository MIT license while retaining upstream notices.



较大改动请先开Issue说明问题。保留已有安全约束与上游协议行为，记录测试范围。回归使用合成文件，日志脱敏。贡献按本仓库MIT许可提交，并保留上游许可。


Use UTF-8 for source and documentation. For C++, keep the existing type-prefixed variable naming, m_ members and _ parameters; preserve comments and keep protocol changes explicit. Windows PowerShell scripts with Chinese text use UTF-8 with BOM.

源码和文档采用UTF-8。C++沿用类型前缀变量、m_成员和_参数命名，保留注释，协议改动需明确说明。包含中文的Windows PowerShell脚本使用UTF-8 BOM。
