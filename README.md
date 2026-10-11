# LanSync

A small Syncthing-style folder sync for your home network. Desktop (Windows, macOS, Linux) and Android.

> **v1 has no encryption or authentication.** Anyone on your network can see shared folder
> contents. Use it only on a trusted home network.

## Download

Prebuilt programs are in [`dist/`](dist):

| Platform | File |
|---|---|
| Android (ARM phones, Android 8+) | `lansync.apk` |
| Windows (64-bit) | `lansync-windows-amd64.exe` |
| macOS Apple Silicon / Intel | `lansync-darwin-arm64` / `lansync-darwin-amd64` |
| Linux x86-64 / ARM64 | `lansync-linux-amd64` / `lansync-linux-arm64` |

The binaries are not code-signed. On macOS/Linux run `chmod +x` on the file first; macOS may need
right-click → Open the first time, and Windows SmartScreen may ask you to confirm. On Android, allow
installing from unknown sources.

## How it works

- Each device runs the same Go core. On desktop it's a background program with a web UI at
  `http://127.0.0.1:8384`; on Android it runs inside the app as a foreground service.
- Devices find each other with a UDP broadcast on the local network (port 21027). If discovery is
  blocked you can type a device's address (`192.168.1.20:22000`) when adding it.
- Files are transferred over plain HTTP between devices (port 22000), whole files at a time.
- Nothing happens automatically: press **Sync now** on a folder. That rescans the folder, pulls
  changes from every device it's shared with, then asks those devices to sync too and waits
  until they've finished (their progress is shown as "*device* is receiving 12/40").

### Folder modes

| Mode | Sends its changes | Receives changes |
|---|---|---|
| Send & receive | yes | yes |
| Send only | yes | no |
| Receive only | only files it received, never local edits | yes |

### Deletes and conflicts

- **Deletes are never sent to other devices.** In a folder that receives, a file you delete comes
  back at the next sync if another device still has it. A send-only folder never gets it back.
- **Conflicts** (a file changed on two devices since the last sync) keep both versions. The main file
  is the newest edit — or, in a receive-only folder, always the sender's version — and the other one is
  saved next to it as `name.sync-conflict-YYYYMMDD-HHMMSS-<device>.ext`.
- Version vectors (per-device edit counters) decide whether a change is a plain update or a conflict.

## Run on desktop

```sh
go run ./cmd/lansync            # opens the web UI in your browser
go run ./cmd/lansync -help      # flags: -data, -gui, -port, -no-browser
./scripts/build-desktop.sh      # binaries for all platforms in dist/
```

## Build the Android app

Needs Go, JDK 17, and the Android SDK with NDK (`ANDROID_HOME`). `.agents/setup` installs all of
these on Debian.

```sh
./scripts/build-android.sh      # -> dist/lansync.apk
```

The release APK is signed with the debug key so it can be installed directly. On first start, allow
notifications and **All files access** (needed to sync arbitrary folders).

## Using it

1. Start LanSync on two devices on the same Wi-Fi. The first time, give each a name you'll
   recognise (e.g. "Vishwash's laptop"); you can change it later with **Rename**. Other devices
   pick up the new name automatically.
2. On each, open **Devices** and add the other one (it shows up under *Discovered*, or add it by ID).
3. On one device, **Add folder**, pick the mode and tick the other device under *Share with*.
4. The other device shows "*X wants to share …*" — accept it, choose a local path and mode.
5. Press **Sync now**.

## Layout

```
core/         engine: config, scanner/index, discovery, peer protocol, sync, local REST API
cmd/lansync/  desktop entry point + embedded web UI (web/)
mobile/       gomobile wrapper used by the Android app
android/      Kotlin + Jetpack Compose app
scripts/      build scripts
```

## Tests

```sh
go test -race ./...
```

`core/sync_test.go` runs several nodes on localhost and checks two-way sync, conflicts, one-way
(send-only → receive-only) behaviour and that deletes are not propagated.
