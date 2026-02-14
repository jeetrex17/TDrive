# TDrive

TDrive is my first Golang project. It’s a small app that uses a **private Telegram channel as “cloud storage”** (upload files → they get posted to the channel, and you can list/download/delete them from the app).

This project is for **educational purposes only**. I’m not trying to harm Telegram, abuse their services, or bypass anything , it’s just a learning project to understand Go + Wails + Telegram APIs.

## How it works (basic idea)

- You login with your Telegram account (phone → code → optional 2FA password).
- The app creates (or reuses) a private channel named `TDrive`.
- Uploading a file = sending it as a document message to that channel.
- Listing files = reading channel message history and extracting documents.
- Downloading = fetching the document by message id.

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
- `tdrive.db` → Local filesystem metadata (folders + which file is in which folder)

## Run (dev)

```bash
wails dev
```

## Build

```bash
wails build
```

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
- [ ] Add file encryption before uploads (privacy reasons ofc)
- [x] Folder support (maybe “virtual folders” metadata)
- [x] Mulitiple files uploads in parallel 
- [ ] Handle uploads/downloads for very large files (Telegram has per file limits, commonly ~2GB unless you are rich and have preimum and if you were rich you woudnt be reading this)
- [ ] Faster downloads 
- [ ] Maybe File sharing (Similar to Grive )
