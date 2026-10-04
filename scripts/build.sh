#!/bin/sh
# Static cross-builds of occulited for every target architecture (D-15). No cgo, ever.
set -eu
cd "$(dirname "$0")/.."
# the version (the commit, -dirty for an uncommitted tree) and the commit from scripts/version.sh
# (task 16); VERSION and COMMIT override them, e.g. VERSION=<commit>-hot for a hot deploy
set -- $(scripts/version.sh)
VERSION=${VERSION:-$1}
COMMIT=${COMMIT:-${2:-}}
mkdir -p dist
for target in linux/amd64 linux/arm64 linux/arm; do
  os=${target%/*}; arch=${target#*/}
  suffix=$arch; [ "$arch" = arm ] && suffix=armv7l; [ "$arch" = arm64 ] && suffix=aarch64; [ "$arch" = amd64 ] && suffix=x86_64
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch GOARM=7 GOFLAGS=-trimpath \
    go build -ldflags "-s -w -X main.version=$VERSION -X main.commit=$COMMIT" -o "dist/occulited-$suffix" ./cmd/occulited
  printf '%-28s %8s KB\n' "dist/occulited-$suffix" "$(( $(stat -c %s "dist/occulited-$suffix") / 1024 ))"
done
echo "occulited $VERSION"
