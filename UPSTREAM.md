# Upstream provenance

Base: [schollz/croc](https://github.com/schollz/croc), tag v11.6.0, commit f707beef66921431eea567887eb94ab7efbfd038. The croc-go directory is a vendored, modified source snapshot. Its upstream LICENSE and THIRD_PARTY_NOTICES remain intact. This repository has independent release tags.

Library integration changes include optional observers/prompt handlers, per-task cancellation and receive roots, structured byte/verification counters, classic transport settings, LAN readiness/port reporting, same-process local lookup, terminal-free hashing, and literal bracket filename handling. bridge/ provides the C ABI and task manager. Update the pinned metadata, notices and interoperability tests together when rebasing.

本项目基于上述Croc版本，croc-go保留已修改源码快照及上游版权。库化改动与上游传输协议兼容性应分别验证。源码文件校验清单见UPSTREAM_FILES.sha256，记录当前补丁后快照。
