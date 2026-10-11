#!/usr/bin/env bash
# Builds the Go core as an Android library (gomobile) and then the APK into dist/.
# Needs ANDROID_HOME with an NDK installed, and JDK 17.
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p android/app/libs dist
# gomobile needs gobind on PATH; install the version pinned in go.mod.
export PATH="$(go env GOPATH)/bin:$PATH"
go install golang.org/x/mobile/cmd/gobind
# ARM only (real phones); add android/amd64 to run in an x86 emulator.
go tool gomobile bind -target=android/arm64,android/arm -androidapi 26 -ldflags "-s -w" -javapkg com.lansync \
	-o android/app/libs/lansync-core.aar ./mobile
(cd android && ./gradlew assembleRelease)
cp android/app/build/outputs/apk/release/app-release.apk dist/lansync.apk
echo "APK: dist/lansync.apk"
