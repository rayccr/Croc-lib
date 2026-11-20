#!/usr/bin/env sh
# Optional Linux build (NOT verified in this round).
# Native amd64:
#   ./scripts/build-linux.sh
# Cross aarch64 (toolchain must be in PATH):
#   GOARCH=arm64 CC=aarch64-linux-gnu-gcc ./scripts/build-linux.sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
ARCH="${GOARCH:-amd64}"
case "$ARCH" in
  amd64) OUT="$ROOT/dist/linux-x64" ;;
  arm64) OUT="$ROOT/dist/linux-arm64" ;;
  *) echo "unsupported GOARCH=$ARCH (use amd64 or arm64)" >&2; exit 2 ;;
esac
CC="${CC:-gcc}"
export GOOS=linux GOARCH="$ARCH" CGO_ENABLED=1 CC
export GOFLAGS=-trimpath
mkdir -p "$OUT"
cd "$ROOT/bridge"
go build -buildmode=c-shared -o "$OUT/libcroc_engine.so" ./cmd/croc_engine
cp "$ROOT/bridge/include/croc_engine_api.h" "$OUT/"
cp "$ROOT/croc-go/LICENSE" "$OUT/"
cp "$ROOT/croc-go/THIRD_PARTY_NOTICES.md" "$OUT/"
echo "Linux output: $OUT"
if command -v readelf >/dev/null 2>&1; then
  readelf -h "$OUT/libcroc_engine.so" | grep -E "Class|Machine|Type"
  readelf -d "$OUT/libcroc_engine.so" | grep -E "NEEDED|SONAME" || true
fi
