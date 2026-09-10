<p align="center">
  <img src="assets/tdrive-banner-loop-main.gif" alt="TDrive: your Telegram-powered drive" width="100%">
</p>

<p align="center">
  <a href="https://github.com/jeetrex17/TDrive/releases/latest"><img src="https://img.shields.io/github/v/release/jeetrex17/TDrive?style=flat-square&color=0e6ba8" alt="Latest release"></a>
  <a href="https://github.com/jeetrex17/TDrive/actions/workflows/ci.yml"><img src="https://github.com/jeetrex17/TDrive/actions/workflows/ci.yml/badge.svg" alt="CI status"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/jeetrex17/TDrive?style=flat-square&color=0e6ba8" alt="License"></a>
  <a href="https://github.com/jeetrex17/TDrive/releases"><img src="https://img.shields.io/github/downloads/jeetrex17/TDrive/total?style=flat-square&color=0e6ba8" alt="Downloads"></a>
</p>

<p align="center">
  <strong>A private, cross-platform drive powered by your own Telegram account.</strong>
</p>

<p align="center">
  Organize files in a desktop app, stream Telegram media, mount your drive in the OS, or manage it from the command line.
</p>

<p align="center">
  <a href="https://github.com/jeetrex17/TDrive/releases/latest"><strong>Download TDrive</strong></a>
  · <a href="#quick-start">Quick start</a>
  · <a href="#features">Features</a>
  · <a href="#how-it-works">How it works</a>
  · <a href="#command-line-interface">CLI</a>
</p>

> [!IMPORTANT]
> TDrive is an independent educational project and is not affiliated with Telegram. Use it responsibly and follow Telegram's terms. Do not treat Telegram or TDrive as the only copy of irreplaceable data.

## Screenshots

<table>
  <tr>
    <td width="33%"><img alt="Photos gallery" src="assets/screenshot-gallery.png"></td>
    <td width="33%"><img alt="Photo viewer" src="assets/screenshot-viewer.png"></td>
    <td width="33%"><img alt="Encrypted photo unlock" src="assets/screenshot-unlock.png"></td>
  </tr>
  <tr>
    <td align="center">Gallery</td>
    <td align="center">File viewer</td>
    <td align="center">Encrypted file unlock</td>
  </tr>
</table>

## Features

### Store and organize

- A private **My Drive** plus collaborative shared drives.
- Nested folders with rename, move, drag-and-drop, search, and delete.
- Files larger than 2 GB, automatically split across Telegram messages and presented as one file.
- Recursive folder downloads with transactional publishing to the destination.

### Import and transfer

- Upload files or whole folders from the desktop app or CLI.
- Import `.zip`, `.tar`, and `.tar.gz` archives, with optional extraction.
- Drop files and folders from the operating system directly into TDrive.
- Concurrent uploads, cancellable transfers, progress, speed, and resilient retries.

### Preview and stream

- Browse photos in a drive-wide gallery.
- Preview images, PDF, text, code, audio, and video without leaving the app.
- Stream media directly from Telegram, including encrypted personal-drive media.
- Native mpv playback for formats the system webview cannot decode, including MKV and HEVC.
- Audio-track, subtitle, playback-speed, picture-fit, and subtitle-appearance controls.

### Share and collaborate

- Shared drives backed by Telegram megagroups.
- Instant-join or approval-required invite links.
- Join-request approval and uploader attribution inside TDrive.
- Collaborative file and folder organization for drive members.

### Mount and automate

- Mount TDrive in Finder, Explorer, or a Linux file manager through a private local WebDAV endpoint.
- Personal drives are read/write, including encrypted content; shared drives are read-only.
- Use the CLI for terminal workflows, scripts, imports, vault access, synchronization, and mounts.
- Interrupted personal-drive writes are journaled and recovered safely.

### Protect and recover

- Optional client-side XChaCha20-Poly1305 encryption for personal-drive file contents.
- Live synchronization while Telegram activity arrives.
- Local SQLite state can be rebuilt from Telegram history after cache or configuration loss.
- Application updates are verified with an Ed25519-signed checksum manifest before installation.

## Download

Get the newest build from [**GitHub Releases**](https://github.com/jeetrex17/TDrive/releases/latest).

| Platform | Desktop app | CLI | Native media | Desktop mount |
| --- | --- | --- | --- | --- |
| macOS Apple silicon | Yes | Yes | Yes | Finder |
| Windows amd64 | Yes | Portable beta | Yes | Explorer |
| Linux amd64 | AppImage | Yes | Yes | GIO/GVfs file managers |

Release assets contain the supported packages for each version. TDrive currently does not publish macOS Intel or Linux ARM desktop builds.

## Quick start

1. Download and install the latest build for your platform.
2. Create your own Telegram API ID and hash at [my.telegram.org/apps](https://my.telegram.org/apps).
3. Start TDrive and enter those credentials in the setup screen.
4. Sign in with your phone number, Telegram login code, and optional 2FA password.
5. Let TDrive create or rediscover **My Drive**.
6. Upload your first file, create a folder, or join a shared drive.

TDrive stores the API ID and hash locally after setup. It never includes a shared project-wide Telegram credential.

### macOS installation

The macOS app is not notarized with a paid Apple Developer certificate. On the first launch, macOS may report that the developer cannot be verified.

1. Try to open TDrive and dismiss the warning.
2. Open **System Settings → Privacy & Security**.
3. Scroll to **Security** and click **Open Anyway**.
4. Authenticate and confirm **Open**.

<p align="center">
  <img src="notopen_mac_err.png" alt="macOS cannot verify the TDrive developer" width="45%">
  <img src="fix_open_err_mac.png" alt="Open Anyway in macOS Privacy & Security" width="45%">
</p>

This approval is normally required only for the first manual installation. Later in-app updates replace the application in place.

## How it works

1. **My Drive** is a private Telegram channel owned by you.
2. **Shared drives** are Telegram megagroups whose members can collaborate.
3. File bodies are sent as Telegram document messages. Large files use multiple immutable parts.
4. Folders, moves, renames, deletes, policies, and encryption information are represented by small `TDX1|...` metadata messages.
5. SQLite is a rebuildable local projection of that Telegram-backed state. TDrive replays Telegram history to synchronize or recover it.

> [!WARNING]
> `TDX1|...` messages are part of the drive history. Do not edit or delete them in the Telegram app; doing so can damage or desynchronize the projected filesystem.

## Privacy and encryption

Encryption is optional and selected per upload on **My Drive**. TDrive encrypts file contents before sending them to Telegram and decrypts them after the correct vault password is entered.

Important limits:

- Encryption currently applies to personal-drive file contents, not shared drives.
- File names, folder names, sizes, Telegram relationships, and operational metadata remain visible.
- Encrypted streams detect modification and truncation, but the current format does not authenticate namespace/control metadata or bind an entire ciphertext object to one filename.
- One vault password protects the encrypted personal files. It stays in memory only until the app or CLI daemon exits or the vault is locked.
- Changing the password re-wraps the existing master key; it does not re-encrypt every stored file.
- There is no password reset. The optional hint cannot decrypt content.

## Shared drives

Shared drives support file and folder upload, download, rename, move, and delete. Members can see who uploaded a file and can organize the shared namespace.

Invite links should be treated like passwords. Use approval-required links when membership needs review, and revoke links that should no longer grant access.

Deleting a shared folder also deletes its contained files after confirmation. Shared drives mount read-only even though they remain writable through the app and CLI.

## Command-line interface

The CLI is available for macOS, Linux, and Windows amd64. Most commands automatically start a local daemon that keeps the Telegram connection, sync state, and unlocked vault key warm between commands.

The GUI and CLI daemon cannot use the same backend state simultaneously. Close the GUI before using CLI commands; the CLI reports this conflict instead of starting a second backend.

### Install on macOS or Linux

```bash
tar -xzf TDrive-*-cli.tar.gz
cd TDrive-*-cli
./install-cli.sh
```

Reload the shell if the installer updated its configuration, then set up and log in:

```bash
tdrive setup --api-id YOUR_ID --api-hash YOUR_HASH
tdrive login +15551234567
tdrive whoami
tdrive ls
```

### Run on Windows

Extract the `*-windows-amd64-cli.zip` release asset and run `tdrive.exe` from PowerShell:

```powershell
.\tdrive.exe setup --api-id YOUR_ID --api-hash YOUR_HASH
.\tdrive.exe login +15551234567
.\tdrive.exe whoami
```

### Common commands

```bash
tdrive drives
tdrive drive use <name|id>
tdrive ls -l
tdrive mkdir -p /Photos
tdrive put photo.jpg /Photos/
tdrive get /Photos/photo.jpg .
tdrive put --extract archive.zip /Imports/
tdrive mv /old /new
tdrive rm -r /folder
tdrive sync
tdrive rebuild
tdrive unlock
tdrive mount
tdrive mount status
tdrive mount stop
```

Folder and archive imports require the destination folder to exist. Single-file uploads can create or rename the final file path.

Shared-drive commands include `drive create`, `drive link`, `drive join`, `drive requests`, `drive approve`, `drive deny`, and `drive leave`. Run `tdrive help` or `tdrive <command> --help` for the complete command reference.

## Desktop mount

Use **Mount** in the app, or close the GUI and run `tdrive mount`. TDrive starts a private localhost WebDAV endpoint and attaches the selected drive to the operating system until it is ejected.

- **macOS:** appears in Finder as **Tdrive personal**.
- **Windows:** defaults to drive `T:`. Windows WebDAV has a default 50,000,000-byte file-size limit that must be changed in the OS for larger files.
- **Linux:** uses the active GIO/GVfs desktop session and requires the GVfs WebDAV backend, such as `gvfs-backends` on Debian or Ubuntu.

Personal-drive mounts support create, replace, rename, move, and delete. Encrypted writes are staged as ciphertext before Telegram commit. Encrypted uploads require a known content length. Shared-drive mounts remain read-only.

Linux mount behavior depends on the desktop's GIO/GVfs integration and should still be treated as beta.

## Updating

TDrive checks GitHub Releases shortly after launch and once each day. Downloads happen in the background, but installation is allowed only after the release checksum manifest passes Ed25519 signature verification using a public key embedded in TDrive.

Open **Check for updates** from the account menu. When an update is ready, select **Restart to update**. On macOS, the same action is available under **Help → Check for Updates…**.

You can disable automatic downloads or skip a version. Update checks contact only `api.github.com` using an anonymous request with no Telegram account data. Development builds created with `wails dev` do not check for updates.

## Known limitations

- TDrive depends on Telegram availability, account access, API behavior, and rate limits.
- It is not a substitute for keeping an independent backup of important files.
- Shared-drive mounts are read-only.
- The Windows CLI is a portable beta without an installer or background system service.
- Folder and archive import is copy-style import, not synchronization or merge; importing the same folder repeatedly may create numbered names.
- `tdrive cat` may stage decrypted content in a temporary file before writing it to standard output.
- macOS Intel, Linux ARM desktop packages, and Apple notarization are not currently provided.

## Build from source

Requirements include Go 1.25, Node.js with npm, Wails v2, and the platform dependencies required by Wails.

```bash
# Backend and CLI
go test ./...
go vet ./...
go build -o tdrive ./cmd/tdrive

# Frontend
cd frontend
npm ci
npm run typecheck
npm run lint
npm test
npm run build

# Desktop development/build
wails dev
wails build
```

Native playback packaging is handled by the scripts under `scripts/` and the release workflow. When a bundled runtime is unavailable, TDrive can fall back to `mpv` from `PATH`; `TDRIVE_MPV_BIN` overrides that binary.

<details>
<summary><strong>Local data locations</strong></summary>

Persistent files live in the operating system's user-config directory:

- macOS: `~/Library/Application Support/TDrive/`
- Linux: `~/.config/TDrive/`
- Windows: `%AppData%\TDrive\`

Important files include:

- `imp_config.json`: Telegram API ID and hash
- `session.json`: Telegram login session
- `config.json`: personal-drive channel configuration
- `tdrive.db`: local projection, sync log, and encryption metadata
- `cli.json`: CLI drive and working-directory state
- `daemon.log`: CLI daemon log
- `backend.lock`: prevents concurrent GUI and daemon ownership

The Unix daemon socket is runtime-only under `$XDG_RUNTIME_DIR` or `/tmp/tdrive-<uid>`. Windows uses a per-user named pipe restricted to the current Windows SID.

</details>

## Project background

TDrive began as my first Go project and a way to learn Go, Wails, and Telegram APIs. I used AI to help with frontend styling, planning, and some implementation areas where the Telegram documentation was difficult, while focusing my learning on the Go and Telegram side.

## Releases and support

- [Latest release](https://github.com/jeetrex17/TDrive/releases/latest)
- [All release notes](https://github.com/jeetrex17/TDrive/releases)
- [Telegram community](https://t.me/Tdrive_community): questions, feedback, and discussion
- [Report a problem](https://github.com/jeetrex17/TDrive/issues)

## License

See [LICENSE](LICENSE).