# Gallery performance contract

The gallery shares its Svelte grid and image broker across desktop, Android and
iOS. An 8 GB phone is the intended mobile baseline. Resource budgets are fixed
working-set limits, never fractions of the number of photos in a drive.

## Data and layout

`GetMediaTimeline` returns month counts and one cursor per 128 photos. SQLite
scans the indexed metadata once to build that sparse timeline. `ListMediaPage`
uses keyset seeks, including near the end of a large library. Cursors bind the
database epoch, drive and projection generation; stale reads trigger refresh.

`GallerySource` retains at most six mobile pages (768 records) or twelve desktop
pages (1,536 records), runs at most two bridge reads, and pins viewport pages
while the viewer navigates elsewhere. The layout
uses binary search over month summaries and constructs only visible rows plus
two buffer rows on each side. A photo anchor preserves position across resize
and metadata refresh. Unmounting a cell releases its image lease.

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

The grid fetches at most 512-pixel thumbnails; the viewer uses at most 1600-pixel
previews. Both travel as binary responses from authenticated loopback sessions.
Neither request path downloads or decodes an original on a cache miss. Missing
previews retain an explicit original-download action. Preview zoom is limited by
preview resolution; full-resolution progressive zoom is not implemented.

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

Existing libraries require the explicit **Create previews** action. It shows a
transfer estimate, processes bounded pages sequentially, supports cancellation,
and resumes by querying missing derivatives. It filters shared-drive ownership,
checks password access before admission, and enforces the admitted source size
before downloading. Unsupported sources receive local, content-bound skip
records so retries can progress. Browsing never starts this bulk operation.

## Regression checks

The relevant tests live alongside the data source, layout, image broker, policy,
viewer and API adapters. `frontend/e2e/app.spec.ts` exercises a 100,000-photo
fixture, bounding DOM nodes and metadata calls while scrolling forward/backward
and jumping with the keyboard. Backend tests cover cursor isolation, revision
changes, cancellation, byte budgets, encrypted derivatives, replay compatibility,
ownership and deletion. The projection benchmark measures first and deep pages
separately from the initial timeline scan.

Browser fixtures and compilation do not replace hardware measurements. Before
release, profile a long scroll on an 8 GB Android phone and an iPhone, including
background/foreground, memory pressure, constrained networking and vault lock.
