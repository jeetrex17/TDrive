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

- Telegram API credentials:
  - macOS: `~/Library/Application Support/TDrive/imp_config.json`
  - Linux: `~/.config/TDrive/imp_config.json`
  - Windows: `%AppData%\\TDrive\\imp_config.json`
- Telegram login session:
  - macOS: `~/Library/Application Support/TDrive/session.json`
  - Linux: `~/.config/TDrive/session.json`
  - Windows: `%AppData%\\TDrive\\session.json`
- Drive channel id (stores `channel_id`):
  - macOS: `~/Library/Application Support/TDrive/config.json`
  - Linux: `~/.config/TDrive/config.json`
  - Windows: `%AppData%\\TDrive\\config.json`

## Run (dev)

```bash
wails dev
```

## Build

```bash
wails build
```

## minor note 
I used AI to help with the frontend UI/styling and planning while i focused more on learning the Go + Telegram side , and also used it for few functions functions like upload coz i wasnt understanding anything from offical docs lol.

## TODOs

- [x] Basic Telegram login (phone/code/2FA)
- [x] Create/reuse a private `TDrive` channel as storage
- [x] Upload, list, download, and delete files
- [x] Stable channel access resolution (no “recent chats” dependency)
- [ ] Add file encryption before uploads (privacy reasons ofc)
- [ ] Folder support (maybe “virtual folders” using filename prefixes or metadata messages)
- [ ] Handle uploads/downloads for very large files (Telegram has per- ile limits, commonly ~2GB unless you are rich and have preimum and if you were rich you woudnt be reading this)
- [ ] Faster downloads 
- [ ] Maybe File sharing (Similar to Grive )
