# Photo and video backup

The backup ledger is separate from the gallery cache and the remote file index.
It records user-selected sources, discovered media versions, and confirmed
upload receipts in `photo-backup.db`, scoped by authenticated account and drive.
The app resolves that scope; native adapters cannot choose an account.

## Execution

- Desktop scans selected folders while TDrive runs. Reconciliation runs at a
  bounded interval and after starting backup. A scan uses `ReadDir(128)` with a
  bounded directory stack, excludes application data, and skips symlinks.
- Android reads authorized MediaStore images/videos through native paged
  queries. iOS reads authorized PhotoKit resources, including the paired video
  of a Live Photo. Neither adapter reads other applications' private files.
- The frontend enumerates mobile sources a page at a time. The Go worker
  uploads one queued resource at a time using the shared file service. Opening
  the gallery does not start an original-library download.
- Mobile discovery runs while the app is active. With a native execution grant,
  an in-flight transfer can continue after backgrounding. Android uses a visible
  data-sync foreground service; iOS grants limited UIKit background time, with a
  conservative Go cancellation deadline. New resources wait for the foreground
  because materialization still needs the WebView. This does not provide a
  headless OS-scheduled uploader or uploads after process termination. Apple's
  background HTTP upload transport is not wired to Telegram.

Settings and queue state survive restart. Desktop scan handles do not: an
interrupted traversal performs a fresh linear scan, with the ledger suppressing
already discovered versions. Mobile discovery may also reconcile the accessible
library again after resume. This is bounded-memory reconciliation, not a promise
of constant-time discovery for a million items.

Pause cancels in-flight work and persists per account and drive. Only an explicit
Resume clears that choice; settings changes, retries, and app restarts do not.
Cancellation without a remote receipt holds the interrupted item for explicit
retry because its remote outcome may be uncertain. Confirmed receipts remain
complete even if cancellation arrives at the same time. Pausing does not spend
the item's failure retry budget.

## Identity and completion

Native identity is account/drive + asset ID + version + resource ID. Selecting
several albums containing the same resource does not duplicate its upload. A
completed receipt remains useful after its original source selection is removed.
Desktop identity uses the selected source and relative file path/version.

Only a positive remote receipt marks a resource complete. A local file-index
write failure after remote commit retains that receipt; ordinary synchronization
repairs the index. If the process dies while an upload is marked in flight, its
remote outcome is uncertain. Recovery holds it for explicit retry instead of
automatically sending another copy. A retry can duplicate an uncertain upload.

Live Photo components are separate uploaded files and separate resource counts.
This version does not publish a compound-asset manifest or reconstruct Live
Photos on restore. It preserves the original resource bytes. Cross-device
content deduplication and cloud album mirroring are not implemented.

## Resource limits and deletion

Staging permits one resource at a time, at most 4 GiB. Native adapters also reserve
512 MiB of free device storage. Staging is streamed, version-checked, canceled
when the worker stops, and released after use. Cold launch removes abandoned
native staging. Desktop staging uses a private snapshot to detect source
replacement and cleans it after upload; it currently relies on write errors for
low-disk detection. The existing uploader may use additional bounded encryption or
multipart scratch files. Larger native resources report an error rather than
silently truncating the original.

The durable queue and catalog grow with the library; thumbnails retain their
independent byte-aware LRU limits. Status counts are maintained transactionally
so refreshing progress does not scan the full queue. A scan page never contains
more than 128 resources.

Backup never deletes device originals. Removing a source stops backing it up
without deleting cloud files. Full logout drains the worker and removes the
backup database, its SQLite sidecars, and native staging; soft logout retains the
ledger. Encryption keys are not stored in backup settings, and encrypted backup
waits for the existing encryption session.

## User-visible limits

Backup source selection is available in the desktop profile menu and mobile
Account tab. The destination is the current drive. Policy controls are enabled
only where native device status can be supplied; an unavailable or stale required
policy prevents uploading.

The gallery shows supported images and videos together. Video tiles request only
document thumbnails and open the existing streaming player on activation. Missing
thumbnails do not trigger full-video downloads. Unsupported preview formats remain
accessible through the normal file browser/download flow. Device albums are not
mirrored into cloud albums.
Original metadata embedded in the file is retained; the current cloud timeline
still orders files by upload time.

Tests cover durable recovery, account isolation, resource deduplication, bounded
folder traversal, cancellation, and 100,000-row transactional status counters.
Native compiler checks and mocked frontend journeys do not replace real-device
permission, low-storage, large-library, and restore testing.
