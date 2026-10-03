# TDrive architecture

These notes explain the implemented systems and the constraints behind them.
They are a reading map for maintainers, not a feature roadmap. Check the current
branch and linked source before assuming a pending feature is available.

| When working on | Read |
| --- | --- |
| Service wiring, GUI/CLI parity, mobile lifecycle or frontend boundaries | [Runtime and platforms](runtime-and-platforms.md) |
| Telegram operations, SQLite replay or synchronization | [Storage and synchronization](storage-and-sync.md) |
| Uploads, retries, mounted writes or deletion recovery | [Transfers and mounted writes](transfers-and-mounts.md) |
| Vault sessions, encrypted streams or authenticated range reads | [Encryption](encryption.md) |
| Playback, multipart range reads or local media URLs | [Media streaming](media-streaming.md) |
| Release verification, installation or previous-copy cleanup | [Desktop updates](desktop-updates.md) |
| Gallery pagination, virtualization, image leases and memory limits | [Gallery architecture and performance](../../frontend/src/ui/gallery/README.md) |
| Durable photo backup, discovery, staging and platform restrictions | [Photo and video backup](../../backend/photobackup/README.md) |
| Incremental Svelte migration and shared UI components | [Svelte UI foundation](../../frontend/src/ui/README.md) |
| Toolchain prerequisites and packaging | [Build guide](../../build/README.md) |

The repository [agent guide](../../AGENTS.md) contains working rules;
[CONTRIBUTING.md](../../CONTRIBUTING.md) defines commit and PR conventions.
Keep detailed explanations here and link them from those guides when needed.

When behavior changes, update the relevant note in the same change. Link to
source files and tests rather than copying large implementations or lists of
version numbers. Test references describe existing coverage, not a claim that
those tests or device checks ran during documentation work.
