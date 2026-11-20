# Ubuntu 构建

在 Ubuntu 24.04 x86_64 上安装 GCC、G++、CMake、Ninja、Go 1.27.1 或更新版本，
以及 Qt 6.10.1 Linux Desktop GCC 64-bit。将两个仓库克隆到同一父目录。
下面从这个父目录执行，按实际位置调整 Qt 前缀。

```bash
sh ./Croc-lib/scripts/build-linux.sh
cmake -S Croc-DawnUI -B Croc-DawnUI/build -G Ninja \
  -DCMAKE_BUILD_TYPE=Release -DCMAKE_PREFIX_PATH="$HOME/Qt/6.10.1/gcc_64" \
  -DCROCD_ENGINE_LIB_DIR="$PWD/Croc-lib/dist/linux-x64"
cmake --build Croc-DawnUI/build --target Croc-DawnUI
cmake -E copy_if_different Croc-lib/dist/linux-x64/libcroc_engine.so Croc-DawnUI/bin/
./Croc-DawnUI/bin/Croc-DawnUI
```

Qt 工程入口是 `Croc-DawnUI/CMakeLists.txt`，Go 模块入口是
`Croc-lib/bridge/go.mod`。Linux 实机收发仍待验证。
