# Gallery performance contract

The gallery shares its Svelte grid and image broker across desktop, Android and
iOS. An 8 GB phone is the intended mobile baseline. Resource budgets are fixed
working-set limits, never fractions of the number of photos in a drive.

## Data and layout

`GetMediaTimelineSummary` returns month counts so the first page can render
before the index scan. `GetMediaTimelineAnchors` then returns one cursor per
128 photos. The generation LRU is capped at eight entries and approximately
4 MiB; oversized snapshots are served without caching. SQLite still scans the
indexed metadata once per cold generation to build navigation anchors. `ListMediaPage`
uses keyset seeks, including near the end of a large library. Cursors bind the
database epoch, drive and projection generation; stale reads trigger refresh.

`GallerySource` retains at most six mobile pages (768 records) or twelve desktop
pages (1,536 records), runs at most two bridge reads, and pins viewport pages
while the viewer navigates elsewhere. The layout
uses binary search over month summaries and constructs only visible rows plus
two buffer rows on each side. A photo anchor preserves position across resize
and metadata refresh. Unmounting a cell releases its image lease.

The physical scroll canvas is capped at 16 million CSS pixels. Logical offsets
map across that canvas, while visible cells retain their actual size. This keeps
the end of a million-photo library reachable under Chromium/WebKit height limits.

## Albums

Photos opens on the folder grid whenever the drive has more than one folder
holding media; one folder, or none, falls back to the single timeline. Tiles
come from one `ListMediaFolders` projection read, in the order it returns them,
recomputed on entry rather than under a live scroll. The grid windows by row
through `ui/file-list/row-window.ts`, so a drive with hundreds of media folders
still costs a viewport of DOM.

Covers are ordinary thumbnails: the same identity the file list builds, the same
broker leases, byte caps, eviction and cancellation. A cover that cannot be
drawn -- absent, a video, a stale revision, a locked vault, offline -- degrades
to a folder glyph or the locked stripes; names and counts are local, so the grid
still navigates.

Opening a tile reuses this same `GallerySource`, scoped by `folderID`. A folder
timeline carries month buckets but no anchor index, so its pages are reached by
following each page's next cursor; the source learns those cursors as pages land
and walks the gap once on a jump. The drive-wide timeline keeps its anchor index
precisely because a million photos cannot be walked.

## Images and ownership

The shared rendition broker owns cancellation, priorities, object URLs and
bounded LRU caches. Memory admission happens before requests and reserves both
encoded bytes and estimated decoded pixels. Browser/GPU memory can exceed those
estimates, so these are broker limits, not a guarantee about total process RSS.

| Budget | Mobile | Desktop |
| --- | ---: | ---: |
| Decoded images | 80 MiB | 192 MiB |
| Encoded images | 8 MiB | 32 MiB |
| Concurrent image requests | 2 | 4 |
| Disposable disk cache | 256 MiB | 1 GiB |

Visible requests take priority. The viewer can prefetch one adjacent preview
only on an explicitly connected, unmetered network without power restrictions.
Backgrounding, vault lock, logout and drive changes release leases and revoke
local image sessions. Native memory-pressure events reduce limits and disable
prefetch for 60 seconds. Session expiry is recoverable; Telegram retry deadlines
survive image-session resets.

The grid fetches at most 512-pixel thumbnails. Opening or navigating the viewer
explicitly opens one revision-bound original stream; neighboring originals are
never prefetched. Images use an 8 MiB stream cache without read-ahead. While an
original is open, the thumbnail broker reduces its budget (16/2 MiB on mobile)
to leave room for browser decoding. Original admission remains limited to 32 MP
and 256 MiB source bytes; browser/GPU overhead is outside these estimates.

Disk LRU admission reserves space before temporary writes and refuses oversized
entries. Failed deletions remain accounted for; undeletable startup overflow is
represented by counters rather than an unbounded in-memory index. Local storage
controls report the disposable cache separately from database/WAL storage.
Cache cleanup is automatic, with no manual clear button. Catalog history and
user downloads are not cache entries.
The cache's initial directory inspection is still proportional to existing files.

Projection rebuilds read and apply 256-row keyset batches within the existing
atomic transaction. They no longer retain the complete operation log in RAM,
but rebuild duration and database/WAL disk usage still grow with history.

## Producing and repairing previews

New supported photo uploads freeze a bounded source snapshot and prepare small
derivatives. ImageIO on Apple platforms and BitmapFactory on Android downsample
before rendering; other desktop hosts use a serialized, pixel-limited decoder.
Sources over 30 MiB and unsupported dimensions/codecs can retain a placeholder.
Derivatives are independently encrypted for encrypted files. Persistent caches
and the bounded retry outbox store their ciphertext, never decrypted originals.

Remote descriptors bind the source content and use an additive field on the
existing hidden-part operation, preserving old-client parsing. Shared-drive
derivatives must come from the original uploader. Replaced content cannot reuse
old derivatives, and deletion includes associated remote messages. Local cache
identity also includes the authenticated account and database epoch.

Old clients can read originals and ignore the additive descriptors, but cannot
delete derivative blobs they do not understand. Cleanup requires an updated
client that still has the source/tombstone history; this is a compatibility
limitation, not a promise that old clients perform complete physical deletion.

Existing libraries receive thumbnails only when cells enter the viewport. The viewer
opens one original-image stream only after an explicit click or navigation; it never
prepares or prefetches originals in the background. The former **Create previews** action
has been removed.

## Regression checks

The relevant tests live alongside the data source, layout, image broker, policy,
viewer and API adapters. `frontend/e2e/app.spec.ts` exercises a 1,000,000-photo
fixture, bounding DOM nodes and metadata calls while scrolling forward/backward
and jumping with the keyboard. Backend tests cover cursor isolation, revision
changes, cancellation, byte budgets, encrypted derivatives, replay compatibility,
ownership and deletion. The projection benchmark measures first and deep pages
separately from the initial timeline scan.

Browser fixtures and compilation do not replace hardware measurements. Before
release, profile a long scroll on an 8 GB Android phone and an iPhone, including
background/foreground, memory pressure, constrained networking and vault lock.
