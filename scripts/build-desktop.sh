#!/usr/bin/env bash
# Builds the desktop daemon (with the embedded web UI) for Windows, macOS and Linux into dist/.
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p dist
for target in windows/amd64 darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do
	os=${target%/*}
	arch=${target#*/}
	ext=""
	[ "$os" = windows ] && ext=".exe"
	echo "building $os/$arch"
	CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w" -o "dist/lansync-$os-$arch$ext" ./cmd/lansync
done
