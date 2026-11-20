#!/usr/bin/env sh
# Optional Android arm64 cross build (POSIX host; NOT verified in this round).
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
: "${ANDROID_NDK_HOME:?set ANDROID_NDK_HOME}"
API="${ANDROID_API_LEVEL:-26}"
HOST_TAG="linux-x86_64"
CC="$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/$HOST_TAG/bin/aarch64-linux-android${API}-clang"
OUT="$ROOT/dist/android-arm64-v8a"
mkdir -p "$OUT"
export GOOS=android GOARCH=arm64 CGO_ENABLED=1 CC
export GOFLAGS=-trimpath
cd "$ROOT/bridge"
go build -buildmode=c-shared \
  -ldflags '-extldflags "-Wl,-z,max-page-size=16384"' \
  -o "$OUT/libcroc_engine.so" ./cmd/croc_engine
cp "$ROOT/bridge/include/croc_engine_api.h" "$OUT/"
cp "$ROOT/croc-go/LICENSE" "$OUT/"
cp "$ROOT/croc-go/THIRD_PARTY_NOTICES.md" "$OUT/"
echo "Android arm64 output: $OUT (not device-verified)."
