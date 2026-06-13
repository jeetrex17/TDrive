<p align="center">
  <img src="assets/banner-clouds.png" alt="TDrive" width="100%">
</p>

<p align="center">
  <a href="https://github.com/jeetrex17/TDrive/releases/latest"><img src="https://img.shields.io/github/v/release/jeetrex17/TDrive?style=flat-square&color=2AABEE" alt="Latest release"></a>
  <img src="https://img.shields.io/github/license/jeetrex17/TDrive?style=flat-square&color=2AABEE" alt="License">
  <img src="https://img.shields.io/github/downloads/jeetrex17/TDrive/total?style=flat-square&color=2AABEE" alt="Downloads">
</p>

TDrive is my first Golang project. It uses Telegram as the storage layer: files are posted into Telegram channels/groups, and the app gives you a drive-like UI on top.

This project is for **educational purposes only**. I’m not trying to harm Telegram, abuse their services, or bypass anything, it’s just a learning project to understand Go + Wails + Telegram APIs.

## Screenshots

<table>
  <tr>
    <td><img alt="Photos gallery" src="assets/screenshot-gallery.png"></td>
    <td><img alt="Photo viewer" src="assets/screenshot-viewer.png"></td>
    <td><img alt="Encrypted photo unlock" src="assets/screenshot-unlock.png"></td>
  </tr>
</table>

## Features

- **Drives & folders** — a private *My Drive* plus collaborative shared drives, with nested folders, rename, move, and delete.
- **Files larger than 2 GB** — automatically split across multiple Telegram messages and rejoined on download; shown as a single file.
- **End-to-end encryption** — optional per-file encryption on My Drive (XChaCha20-Poly1305); contents are encrypted before they ever reach Telegram.
- **Folder & archive import** — drop whole folders or `.zip` / `.tar` / `.tar.gz` archives; folder structure is recreated and archives can be extracted on the way in.
- **Photos gallery** — browse every image in a drive, newest first.
- **Shared drives** — invite friends with instant or approval-required links; everyone can upload, organize, and see who uploaded what.
- **Cancellable transfers** — cancel uploads and downloads mid-flight, with live size and speed.

## Download

Grab the latest build for your platform from the [**Releases**](https://github.com/jeetrex17/TDrive/releases/latest) page (macOS, Windows, and Linux AppImage). On first run, enter your Telegram API credentials (see [Telegram API ID + Hash](#telegram-api-id--hash-required) below).

### macOS installation note

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

## Telegram API ID + Hash (required)

You need your own Telegram API credentials:
- Get them from: https://my.telegram.org/apps

The app stores these credentials locally after you enter them in the setup screen.

## Build from source

```bash
wails dev      # run in development
wails build    # build a release binary
```

You’ll need your own Telegram API credentials (see above) the first time you run it.

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

## Notes & caveats

**TDX metadata.** You will see `TDX1|...` messages in Telegram. They are silent but visible, and they are how TDrive remembers the drive structure. Don’t edit or delete them from the regular Telegram app. Changing them can desync what different members see.

**Download counts.** The downloads badge shows total GitHub release asset downloads. For a per-release/per-OS breakdown:

```bash
./scripts/release-downloads.sh
./scripts/release-downloads.sh v1.0.0
```

This uses GitHub's release asset download counts through the `gh` CLI. It does not track active users.

**A note on AI.** I used AI to help with the frontend UI/styling and planning while i focused more on learning the Go + Telegram side , and also used it for few functions like upload coz i wasnt understanding anything from offical docs lol.

## License

See [LICENSE](./LICENSE).
