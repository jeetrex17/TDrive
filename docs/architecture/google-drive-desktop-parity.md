# Google Drive for desktop parity

## Goal

Provide a Windows desktop-drive experience for TDrive, using Google Drive for
desktop as a behavioral reference while keeping Telegram as the remote storage
backend. This is a TDrive feature, not a Google Drive integration.

The target is a drive in File Explorer with cloud-backed files, on-demand reads,
local cache, explicit offline pinning, optional full mirroring, safe
synchronization, and visible transfer state.

## Reference behavior

Google Drive for desktop documents two modes for My Drive:

- **Stream:** cloud remains the primary copy. Files are read on demand; local
  disk is used for opened/recent files and items explicitly made available
  offline. Streamed content requires the desktop app and a connected account.
- **Mirror:** a complete local copy is kept alongside the cloud copy. It uses
  local disk and remains available offline, including when the desktop app is
  not running.
- In stream mode, a file or folder can be marked available offline. Pinning a
  folder applies to its contents and newly added children. Unpinning removes
  the local offline copy without deleting the cloud item.
- Changes made in the cloud or local mirrored copy are reflected across devices.
  Conflicting local and cloud content must be preserved rather than silently
  overwritten.

References:
- https://support.google.com/drive/answer/13401938
- https://support.google.com/drive/answer/2375012
- https://support.google.com/drive/answer/16631477

## Existing TDrive foundations (verified in source)

- backend/mountfs exposes TDrive channel contents as a filesystem.
- backend/mountcontent resolves Telegram documents and serves random-access
  reads through Telegram range requests. It has bounded peer/document-reference
  caches and read-ahead; the document-reference cache is not a durable offline
  file-content cache.
- backend/mountdav serves the local WebDAV endpoint.
- backend/mountos attaches that endpoint to the host. Windows currently maps
  the endpoint as a WebDAV network drive through the WebClient service.
- backend/mountcontroller owns mount lifecycle, encryption key leases,
  writable-session draining, and optional aggregation of selected drives under
  one virtual root. The current controller owns one mounted attachment.
- backend/mountfs caches directory snapshots. That cache is metadata, not a
  complete content cache.
- Writable mounted operations use the existing journaled writer and must retain
  its uncertain-commit and recovery semantics.

## Proposed TDrive behavior

### 1. Stream mode (default)

- Show cloud namespace entries in File Explorer without downloading every body.
- Fetch file bytes on demand through the existing range-reader path.
- Add a bounded, disk-backed content cache with integrity checks, eviction,
  account/drive scoping, and safe invalidation when a remote revision changes.
- Keep in-progress writes staged and journaled; publish only through the
  existing mounted-write commit path.
- Show online-only, cached, downloading, syncing, and error states.

### 2. Offline availability

- Add an explicit pin/unpin operation for files and folders.
- Folder pins apply recursively and to new children.
- Download pinned content to a configurable local cache/offline root.
- Persist pin intent and download checkpoints so restart/reconnect can resume.
- Unpinning removes only TDrive-owned cache copies after active readers release
  them; it must never delete remote Telegram content.
- Report insufficient disk space and partial/offline failures explicitly.

### 3. Mirror mode

- Maintain a complete local copy of the selected personal drive or selected
  folders.
- Keep local changes and remote changes reconciled with stable identities,
  content hashes/revisions, and a durable sync journal.
- Preserve both sides on conflicts; never silently replace a newer or
  unrecognized local file.
- Deletions require tombstones/confirmed intent and recovery-safe handling.
- Mirror mode consumes local disk space and must report its actual footprint.

### 4. Multiple independent mounts

- Support independently configured drive letters (for example T:, U:, V:),
  each bound to one immutable drive selection and its own lifecycle/status.
- Reject occupied letters and prevent one mount's operations or cache from
  being redirected when the active TDrive UI drive changes.
- Keep the existing single-root multi-drive aggregate behavior available where
  useful; it is not a substitute for independent mounts.

### 5. Virtual capacity

- Display TDrive's configured logical capacity and derived used/free values,
  not the host C: drive capacity.
- Initial proposal: 200 GB capacity, expand by 100 GB when 50 GB remains.
- This is a logical presentation limit only. It does not reserve disk space,
  create Telegram quota, or guarantee unlimited remote storage.
- Validate capacity calculations against actual stored bytes and Telegram
  upload/account constraints; make the policy configurable.

### 6. Explorer integration

- Provide context actions for Make available offline and Remove offline copy.
- Expose status and transfer progress without leaking local endpoint capability
  tokens or credentials.
- Evaluate Windows shell integration for status overlays and context menus
  separately from the filesystem protocol; avoid adding a shell extension until
  its install, update, and uninstall lifecycle is defined.

## Implementation sequence

1. **Baseline and contract tests:** map the current mount, reader, writer,
   lifecycle, cache, sync, and Windows connector paths; record supported
   semantics and missing behavior.
2. **Independent mount lifecycle:** introduce a manager that owns multiple
   isolated mount controllers and persistent per-mount configuration. Keep the
   existing controller's single-attachment lifecycle intact.
3. **Disk content cache:** implement bounded, revision-aware cache blocks or
   complete-file entries behind the existing content reader interface.
4. **Offline pinning:** persist pin intent, recursively enumerate folders,
   download/resume content, and expose safe pin/unpin operations.
5. **Mirror and backup:** implement durable two-way reconciliation, conflict
   preservation, and safe deletion semantics.
6. **Explorer UX:** add shell context actions/status integration and transfer
   progress.
7. **Logical capacity:** add settings and capacity reporting after used-byte
   accounting is authoritative.
8. **Verification:** run Go tests/race tests, frontend checks, Windows builds,
   and real Windows File Explorer integration tests with the WebClient/WebDAV
   provider. Evaluate WinFsp only if measured WebDAV limitations block required
   behavior.

## Safety and correctness invariants

- Telegram history and file bodies remain the remote source of truth.
- SQLite remains a rebuildable projection with durable recovery evidence; do not
  bypass the projection pipeline.
- A failed network request does not prove that a remote write was rejected.
- Never delete a local or remote copy until ownership and commit state are known.
- Keep account, channel, drive, and revision identity on every cache/sync job.
- Never expose or log loopback mount capability URLs, Telegram sessions, vault
  keys, passwords, or decrypted content.
- Offline and mirror claims must be verified on a real disconnected Windows
  machine; CI compilation alone is not sufficient.
