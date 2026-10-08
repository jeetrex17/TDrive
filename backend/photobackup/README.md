# Photo and video backup

The backup ledger is separate from the gallery cache and the remote file index.
It records user-selected sources, discovered media versions, and confirmed
upload receipts in `photo-backup.db`, scoped by authenticated account and drive.
The app resolves that scope; native adapters cannot choose an account.

Current app backup is restricted to **My Drive and encrypted uploads**. The
[access gate](../../internal/app/photo_backup_access.go) validates the personal drive;
[uploadPhotoBackup](../../internal/app/photo_backup_upload.go) forces encryption even for work
queued by older builds. A legacy settings value cannot enable plaintext backup.

## Execution

- Desktop scans selected folders while TDrive runs. Reconciliation runs at a
  bounded interval and after starting backup. A scan uses `ReadDir(128)` with a
  bounded directory stack, excludes application data, and skips symlinks.
- Android reads authorized MediaStore images/videos through native paged
  queries. iOS reads media files in user-selected folders through the system
  document picker and persisted bookmarks. Neither adapter reads other
  applications' private files without a user-granted access path.
- An Android folder source is a place, not a document tree. The system picker
  chooses the folder; the tree it returns is converted to a media volume and a
  path on it, and the folder is then read through the same paged MediaStore
  query. Nothing holds a URI permission, so a folder source cannot be lost to a
  revoked tree grant. Media permissions still determine which items can be read.
  Such a source covers the photos and videos MediaStore indexes, not every
  file in the folder, and only folders on the device's own storage: one from a
  cloud provider is refused where it is picked. Overlapping folders are refused
  because both would dedupe to one upload whose destination
  depended on scan order. iOS uses a different mechanism: the folder picker
  retains a bookmark and opens its security scope when accessing the folder.
  A missing or unresolvable bookmark makes that source unavailable.
  Only `folder` and `device-folder`
  sources are accepted; retired album and whole-library sources and their
  unfinished work are removed on startup. Completed receipts and uploaded files
  are kept.
- The frontend enumerates mobile sources a page at a time. Each page and its
  continuation cursor are committed together to the Go ledger. A stopped scan
  resumes from that checkpoint when the native cursor remains valid; an expired
  native cursor restarts the source and the ledger deduplicates earlier pages.
  Two Go workers can prepare and upload queued resources concurrently using the
  shared file service. Their combined staged originals cannot exceed 4 GiB;
  a large resource may therefore serialize the pipeline. Opening the gallery
  does not start an original-library download.
- Mobile discovery runs while the app is active. With a native execution grant,
  an in-flight transfer can continue after backgrounding. Android uses a visible
  data-sync foreground service; iOS grants limited UIKit background time, with a
  conservative Go cancellation deadline. New resources wait for the foreground
  because materialization still needs the WebView. This does not provide a
  headless OS-scheduled uploader or uploads after process termination. Apple's
  background HTTP upload transport is not wired to Telegram.

The app owns scheduling and policy; this package owns discovery checkpoints,
job state and receipts. Mobile bytes cross the native bridge only when requested
for the current job:

```mermaid
sequenceDiagram
    participant Host as Native folder adapter
    participant UI as Foreground controller
    participant App as App worker
    participant Ledger as Backup ledger
    participant Files as Shared file service
    UI->>Host: Enumerate selected source
    Host-->>UI: Metadata page, up to 128 resources
    UI->>App: Enqueue page for current account and drive
    App->>Ledger: Deduplicate and persist pending resources
    App->>Ledger: Claim one eligible resource as uploading
    App-->>UI: Request materialization with correlation token
    UI->>Host: Stage selected resource, check version and limits
    Host-->>UI: Private cache path and staging token
    UI->>App: Resolve requested resource
    App->>Files: Upload encrypted original to destination
    Files-->>App: Remote message receipt or error
    App->>Ledger: Persist completion or retry/held state
    App-->>UI: Release staged resource
    UI->>Host: Cleanup or cancel staging
```

Settings and queue state survive restart. Desktop scan handles do not: an
interrupted traversal performs a fresh linear scan, with the ledger suppressing
already discovered versions. Mobile discovery checkpoints survive restart, but
iOS directory iterator tokens are process-local and require a fresh traversal
after expiry. Desktop and Android page their traversal/query work. Large scans
take time proportional to the number of media files; none promises constant-time
discovery for a million items.

Pause cancels in-flight work and persists per account and drive. Only an explicit
Resume clears that choice; settings changes, retries, and app restarts do not.
Cancellation before any original send returns the item to pending without
spending its retry budget. Cancellation after a send without a remote receipt
holds the interrupted item for explicit retry because its remote outcome may be
uncertain. Confirmed receipts remain
complete even if cancellation arrives at the same time. Pausing does not spend
the item's failure retry budget.

## Identity and completion

Native identity is account/drive + asset ID + version + resource ID. Repeated
discovery of the same resource does not duplicate its upload. A completed
receipt remains useful after its original source selection is removed.
Desktop identity uses the selected source and relative file path/version.

[`RunNext`](engine.go) atomically claims at most one eligible job in a short SQLite
transaction, released before staging or network work. Its result distinguishes
an empty queue from a processed failure and a deferred, unsent item. `RunOnce`
remains a single-job compatibility wrapper. Ordinary failures enter `error`
with exponential backoff (default base: one minute); reaching the default eight
attempts moves the job to `paused`. Explicit Retry resets `error`, `paused` and
`missing` jobs to `pending`, including their attempt counters. This per-job state
is separate from the persisted manual-pause setting, which only Resume clears.

Backoff begins when the failed attempt finishes, not when it was claimed.
An uncertain Telegram send is held as `paused` even if the caller's context is
still live. In-call retries preserve the same send identity; automatic ledger
retries must not invent a new identity for an uncertain accepted upload.

Only a positive remote receipt marks a resource complete. A local file-index
write failure after remote commit retains that receipt; ordinary synchronization
repairs the index. If the process dies while an upload is marked in flight, its
remote outcome is uncertain. Recovery holds it for explicit retry instead of
automatically sending another copy. A retry can duplicate an uncertain upload.
If writing the receipt to the backup ledger itself fails, crash recovery can
still find an ambiguous `uploading` row;
the local file index and the backup receipt are separate persistence steps.

A receipt also stops counting as complete once the drive no longer holds the
file it points at. A sweep checks up to 2,048 receipts before each run in batches
of 256, from a durable cursor that rewinds at the end of a cycle. The
[app comparison](../../internal/app/photo_backup_receipts.go) reads the local drive
projection and trash entries; it does not probe each message directly in Telegram.
A receipt reported absent becomes `missing`: the ledger stops counting it as
backed up and the panel reports it as waiting on the user. That state is never
queued automatically; only explicit Retry sends the file again, preserving a
deliberate deletion until the user chooses otherwise.
A file in the trash is still recoverable and is not a loss, a file that comes
back becomes complete again. IDs above the greatest message ID in the local
file index are deferred.
This frontier guard is not proof that every earlier ID has been indexed: receipt
reconciliation depends on the projection having caught up with remote changes.

A watched folder's subfolders are recreated under its destination. A desktop
walk derives them from the file's own path; a phone has no path to derive them
from, because the bytes are staged in the app's cache, so the host reports the
folder chain with each discovered item and the ledger carries it. Either way
the chain is sanitized component by component before it becomes a folder name.

The current iOS discovery path enumerates selected folders, not the PhotoKit
library or albums. [`TDrivePhotoBridge.inc`](../../build/ios/TDrivePhotoBridge.inc)
stores bookmarks in `NSUserDefaults`, keyed by normalized `files:<path>/` roots.
Each scan/staging access resolves the bookmark and opens a security scope;
stale but resolvable bookmarks are refreshed. A missing bookmark, failed
resolution or denied scope makes the source unavailable.

The iOS adapter streams a selected folder through a security-scoped directory
iterator. It retains one lookahead resource and returns at most 128 resources
per page, without a fixed file-count cap or an in-memory listing of the entire
folder. Hidden files and package descendants are skipped. A live iterator token
advances on each page and is released at completion or when the app backgrounds.
After an expired token or process restart, discovery starts at the folder root;
the ledger skips media versions it has already queued or uploaded.
There is no compound-asset manifest or Live Photo reconstruction. Cross-device content deduplication and cloud album mirroring
are not implemented.

## Resource limits and deletion

Staging permits at most two resources with an aggregate 4 GiB reservation.
Unknown-size resources reserve the whole backend budget. Native acquisition is
serialized, but a completed stage can upload while the next resource is copied.
Native adapters also retain 512 MiB of free device storage. Staging is streamed,
size- and version-checked, and released after use. Android copy cancellation is
cooperative; the iOS coordinated provider copy is synchronous and cannot be
interrupted by JavaScript, so canceled work is cleaned after it returns.
Cold launch removes abandoned
native staging. Desktop staging uses a private snapshot to detect source
replacement and cleans it after upload; it currently relies on write errors for
low-disk detection. The uploader may additionally hold one ciphertext part per
worker (at most 1900 MiB each, or a smaller whole-file ciphertext), so 4 GiB is
the original-stage limit, not the total temporary-disk footprint. Other app
transfers have their own scratch usage. Larger native resources report an error rather than
silently truncating the original.

The durable queue and catalog grow with the library; thumbnails retain their
independent byte-aware LRU limits. Status counts are maintained transactionally
so refreshing progress does not scan the full queue. A scan page never contains
more than 128 resources.

Backup never deletes device originals. Removing a source stops backing it up
without deleting cloud files. Full logout drains the worker and removes the
backup database, its SQLite sidecars, and native staging; soft logout retains the
ledger. Encryption keys are not stored in backup settings. Backup waits for the
existing encryption session.

When encryption is locked, the panel shows the unlock prerequisite before
starting. Explicit start, resume, or retry actions use the existing password
dialog; automatic discovery never opens a password prompt. Canceling the dialog
leaves the operation stopped. Passwords and keys remain session-only.

The ledger is currently schema v8. Sequential migrations add manual pause,
remove charging policy, and add capture time, receipt cursors, relative folder
paths, scan checkpoints and a partial FIFO claim index. Backup has no charging condition; migrations preserve
supported sources and jobs, followed by cleanup of retired sources and orphaned
unfinished work.

## User-visible limits

Backup source selection offers selected folders on desktop, Android and iOS
when the corresponding native picker is available. Under Android's partial
photo access, MediaStore answers only with the
items the user authorized. Adding a folder requests media access as well as
the folder location. New uploads go to `Photo backup / <device> / <source>` in
My Drive (under a configured destination parent when present). Device names
are persisted with an installation-specific suffix. Indexed folder lookup reuses
the hierarchy across restarts; existing completed uploads are not moved or sent
again. The panel reports the actual destination layout.

A single scoped notification/Transfers row reports the current filename, or
"2 files" with byte-weighted aggregate transport progress when both are active.
Byte updates are throttled to four per second; completion removes only that
worker's progress slot. Two fixed slots keep state bounded independently of the
queue, and stale callbacks cannot restore another file or drive's progress.

Encrypted photo backups hand off the uploader's owned immutable snapshot (up to
30 MiB) to a run-scoped preview pipeline, releasing the original upload slot and
native source independently of preview sends. One worker prepares encrypted
derivatives; another resumes/sends the durable encrypted outbox. The preview
worker reserves at most two snapshots totaling 30 MiB, including anticipated
snapshots for originals still uploading. Temporary uploader copies and existing
decode budgets are additional memory. Admission applies cancellable backpressure
**before** acquiring an upload slot or sending an original if preparation falls
behind; it never delays a known receipt waiting for capacity. Images larger than
15 MiB may serialize under this preview reservation. Admission does not wait for
preview network sends. Original receipts remain authoritative even if optional
previews fail.
Pause, lock, drive switch and logout cancel/join the pipeline and clear retained
plaintext. Persisted ciphertext resumes on the next backup run without the
source file or key. Natural completion drains preview work after original
receipts are saved. The existing outbox cap is 128 entries/64 MiB. A full outbox
or preparation failure can leave optional derivatives missing; explicit gallery
preparation remains available, not an automatic fallback. Preview failure never
invalidates the original receipt.
Unlocking encryption immediately retries visible locked thumbnails; offscreen
items remain lazy. Older encrypted images without derivatives can prepare them
on demand, capped at 30 MiB per original and by thumbnail worker concurrency.
Temporary originals stay encrypted on disk and derivatives use the encrypted
cache. Larger or unsupported images may still lack a thumbnail.

Wi-Fi-only policy is supported on Android. While visible, the controller refreshes
connectivity every 30 seconds during enabled, unpaused Wi-Fi-only backup, even
after discovery finishes. A required report older than two minutes, or an
unavailable report, prevents new uploads; other platforms reject that setting.

The gallery shows supported images and videos together. Video tiles request only
document thumbnails and open the existing streaming player on activation. Missing
thumbnails do not trigger full-video downloads. Unsupported preview formats remain
accessible through the normal file browser/download flow. Device albums are not
mirrored into cloud albums.
Original metadata embedded in the file is retained; the current cloud timeline
still orders files by upload time.

The iOS folder path is implemented in
[TDrivePhotoBridge.inc](../../build/ios/TDrivePhotoBridge.inc), reached through
the [native adapter](../../frontend/src/modules/photo-backup/native-adapter.ts)
and [backup controller](../../frontend/src/modules/photo-backup/controller.ts).
Folder access depends on the selected provider and a valid security scope;
the presence of the bridge does not establish behavior on every physical device.

Tests cover durable recovery, account isolation, resource deduplication, bounded
folder traversal, cancellation, and 100,000-row transactional status counters.
Native compiler checks and mocked frontend journeys do not replace real-device
permission, low-storage, large-library, and restore testing.

## Performance evidence and measurement

The original workers share the existing file-service semaphore and Telegram
chunk limiter with manual uploads. This change does not raise transport threads
or connections. Two originals are a conservative resource choice, not a claim
that two is optimal on every device or network.

Per-run logs report original-completion wall time, completion count, known
original bytes, peak staged-byte reservations and time including preview drain.
Fixed logarithmic histograms report stage counts, total/max duration and p50/p95
bucket upper bounds for budget waits, materialization, destination resolution,
ledger work, encryption, transport, projection and previews. They retain no
per-file samples or paths. Transfer timing includes retry/backoff; encryption
includes ciphertext staging. Stages overlap across workers: their sums are not
wall time or throughput. Whole-original timing includes the separately reported
pre-send preview-admission wait when backpressure applies.

Repeatable offline comparisons:

```sh
go test ./backend/photobackup -run '^$' -bench 'BenchmarkClaim' -benchmem -count=5
go test ./backend/services/file -run '^$' -bench '^BenchmarkBackupPreviewPipeline$' -benchtime=20x -benchmem -count=5
go test ./internal/app -run '^$' -bench '^BenchmarkPhotoBackupQueueLatency$' -benchtime=10x -benchmem -count=5
```

The service benchmark uses real encryption/SQLite, two tiny JPEGs and an offline
Telegram fake; it requires all four derivatives in both variants. The scheduler
benchmark uses simulated waits and compares one, two and three workers. Neither
measures real Telegram throughput, physical-device memory or battery usage.

An illustrative local run on Apple M4/macOS arm64 (2026-10-08, five repetitions
of 20 two-image batches) produced these medians and observed ranges:

| Offline service measure | Synchronous previews | Queued previews |
| --- | --- | --- |
| Both original calls returned | 5.23 ms (4.78–5.95) | 3.70 ms (2.89–7.98) |
| All work, including four derivatives | 5.23 ms (4.78–5.95) | 5.52 ms (4.46–12.28) |
| Allocated bytes per batch | 886,032 | 898,590 |

These noisy small-file results show earlier original returns, not a demonstrated
total-time improvement. The queued path has additional coordination/allocation
cost. Separately, the real-SQLite claim/requeue benchmark with 10,000 ready jobs
fell from about 7.84 ms before the claim-index optimization to 0.35 ms; the
one-job case increased from about 0.30 to 0.33 ms. Candidate selection still scans
disabled-media/source prefixes and sorts eligible errors; it is not constant
time for every queue shape. The benchmark includes these adverse-prefix cases.

For device evaluation, use the same consented photo-heavy, video-heavy and mixed
corpora, fresh isolated ledgers/destinations, unchanged transport limits and
repeated interleaved baseline/candidate runs. Record original durable-completion
time separately from preview drain, bytes/second, CPU, peak RSS and scratch disk,
retries/flood waits, and cancellation-to-quiescence time. Include constrained
storage, Wi-Fi changes, foreground/background transitions and competing manual
uploads. Do not select a worker count from simulated latency alone or treat
cumulative allocated bytes (`B/op`) as peak resident memory.
