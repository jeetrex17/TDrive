# TDrive

![GitHub Downloads](https://img.shields.io/github/downloads/jeetrex17/TDrive/total)

TDrive is my first Golang project. It uses Telegram as the storage layer: files are posted into Telegram channels/groups, and the app gives you a drive-like UI on top.

This project is for **educational purposes only**. I’m not trying to harm Telegram, abuse their services, or bypass anything, it’s just a learning project to understand Go + Wails + Telegram APIs.

## How it works

- You login with your Telegram account (phone → code → optional 2FA password).
- **My Drive** is a private Telegram channel owned by you.
- **Shared drives** are Telegram megagroups, so friends can join and upload too.
- Uploading a file = sending it as a Telegram document message.
- Folders, renames, moves, deletes, encryption settings, etc. are stored as small `TDX1|...` metadata messages.
- SQLite is only a local cache. If the cache is wiped, TDrive can rebuild the view from Telegram history.

## Shared drives

Shared drives are usable now, but still new. You can:

- Create a shared drive and invite friends with a link.
- Choose normal instant-join links or approval-required links.
- Approve/reject join requests from inside TDrive.
- Upload, download, rename, move, and delete files.
- Create, rename, move, and delete folders.
- See who uploaded files.

Treat invite links like passwords. Anyone with a normal invite link can join unless you revoke it. If you need tighter control, create an approval-required link.

Folder structure inside a shared drive is collaborative: any member can create, rename, move, or delete folders. Deleting a folder also deletes the files inside it, after a confirmation warning.

## Encryption

Personal-drive encryption is per upload. When you choose encrypted upload, TDrive encrypts the file contents before sending them to Telegram, and decrypts automatically on preview/download after you enter the correct password.

Important details:

- Encryption is for **My Drive only** right now, not shared drives.
- Only file contents are encrypted. File names, folder names, sizes, and metadata are still visible.
- One encryption password protects all encrypted personal files.
- TDrive remembers the password only until you close the app.
- Changing the password does not re-encrypt every file. It re-wraps the same master key, so old encrypted files still work with the new password.
- There is no reset/recovery if you forget the password. The hint can help you remember, but it cannot decrypt anything by itself.

## TDX metadata warning

You will see `TDX1|...` messages in Telegram. They are silent but visible, and they are how TDrive remembers the drive structure. Don’t edit or delete them from the regular Telegram app. Changing them can desync what different members see.

## Telegram API ID + Hash (required)

You need your own Telegram API credentials:
- Get them from: https://my.telegram.org/apps

The app stores these credentials locally after you enter them in the setup screen.

## Where data is stored (local files)

Everything is stored inside your OS “user config” folder under a `TDrive` directory:

- macOS: `~/Library/Application Support/TDrive/`
- Linux: `~/.config/TDrive/`
- Windows: `%AppData%\\TDrive\\`

Files you’ll see there:
- `imp_config.json` → Telegram API ID + Hash (from the setup screen)
- `session.json` → Telegram login session
- `config.json` → Drive channel id (`channel_id`)
- `tdrive.db` → Local cache for channels, folders, files, sync log, and encryption metadata

## Run (dev)

```bash
wails dev
```

## Build

```bash
wails build
```

## Release download counts

The README badge shows total GitHub release asset downloads. For a detailed per-release/per-OS breakdown:

```bash
./scripts/release-downloads.sh
./scripts/release-downloads.sh v1.0.0
```

This uses GitHub's release asset download counts through the `gh` CLI. It does not track active users.

## macOS Installation Note

If you see a warning saying **"TDrive Not opened because the developer cannot be verified"** this is normal for open source apps not signed with a paid Apple ID ( they ask for 99$/yr certificate and i aint got that money to spent it on that).

Example of the popup :

![TDrive not opened warning on macOS](./notopen_mac_err.png)

**To fix this:**

1.  Try to open **TDrive** (click "OK" on the error popup).
2.  Open your Mac's **System Settings** (or System Preferences).
3.  Go to **Privacy & Security**.
4.  Scroll down to the **Security** section.
5.  Click the **Open Anyway** button.
6.  Enter your password if asked, and click **Open**.

This is what it looks like in **Privacy & Security**:

![Open Anyway button in Privacy & Security on macOS](./fix_open_err_mac.png)

*(You only need to do this once. Future opens will work normally).*

## Minor note 
I used AI to help with the frontend UI/styling and planning while i focused more on learning the Go + Telegram side , and also used it for few functions functions like upload coz i wasnt understanding anything from offical docs lol.

## TODOs

- [x] Basic Telegram login (phone/code/2FA)
- [x] Create/reuse a private `TDrive` channel as storage
- [x] Upload, list, download, and delete files
- [x] Rename/move files and folders
- [x] Stable channel access resolution (no “recent chats” dependency)
- [x] Personal-drive file encryption before upload
- [x] Folder support (maybe “virtual folders” metadata)
- [x] Shared drives with invite links
- [x] Approval-required shared drive links
- [x] Multiple file uploads in parallel
- [ ] Handle uploads/downloads for very large files (Telegram has per file limits, commonly ~2GB unless you are rich and have premium and if you were rich you wouldn't be reading this)
- [ ] Faster downloads
- [ ] Real-time sync instead of manual refresh
- [ ] Better handling for files posted directly from Telegram
- [ ] Missing-file warnings if Telegram message bodies are deleted outside TDrive
