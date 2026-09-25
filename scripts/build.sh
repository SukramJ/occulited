#!/bin/sh
# Static cross-builds of occulited for every target architecture (D-15). No cgo, ever.
set -eu
cd "$(dirname "$0")/.."
VERSION=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}
mkdir -p dist
for target in linux/amd64 linux/arm64 linux/arm; do
  os=${target%/*}; arch=${target#*/}
  suffix=$arch; [ "$arch" = arm ] && suffix=armv7l; [ "$arch" = arm64 ] && suffix=aarch64; [ "$arch" = amd64 ] && suffix=x86_64
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch GOARM=7 GOFLAGS=-trimpath \
    go build -ldflags "-s -w -X main.version=$VERSION" -o "dist/occulited-$suffix" ./cmd/occulited
  printf '%-28s %8s KB\n' "dist/occulited-$suffix" "$(( $(stat -c %s "dist/occulited-$suffix") / 1024 ))"
done
