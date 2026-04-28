# TDrive Shared Drives — Full Implementation Plan

> Living document. Captures every decision made during design discussion, every
> edge case considered, every invariant locked. Read top to bottom before
> writing code; refer back to it during review.

---

## 1. The Feature

A group of friends can share a common file & photo database inside TDrive
without TDrive running any server. Each "shared drive" is a Telegram megagroup
that members create or join via a Telegram invite link. Files uploaded by any
member appear for everyone, organized in a folder tree that stays in sync
across all members' machines.

### What users get

- A **sidebar** showing their personal drive (today's experience, unchanged)
  plus every shared drive they've joined.
- **Create shared drive** → get an invite link → send to friends.
- **Join via link** → paste a `t.me/+...` link → drive appears, syncs.
- **Upload, browse, preview, download, delete, organize** in any drive — same
  UX as today, scoped to whichever drive is active.
- **Uploader chips** ("Jeet · 2h ago") on files in shared drives.
- **Unread dots** when new content arrives in a shared drive since last visit.
- **Share / leave / kick** controls following Telegram's own admin model.

### What we are NOT building in v1

- A server, account system, or any backend beyond Telegram.
- Per-file ACLs, viewer-only roles, or app-level permissions. Telegram
  membership = access.
- Native Telegram photo (`MessageMediaPhoto`) ingestion as first-class. v1
  uploads images as documents (full quality, filename preserved). Photos
  posted via the regular Telegram app surface in an "Unmanaged" bucket.
- Offline op queueing. v1 requires online for any action.
- Cross-drive drag-and-drop. Personal and shared drives are strictly separate.
- Snapshots / log compaction. Initial sync cost grows with channel history.
  Acceptable for v1; v2 item.

---

## 2. Why Telegram-as-backend works (and where it breaks)

Telegram already provides:

- **Membership & invites** — `MessagesExportChatInvite`, `MessagesImportChatInvite`.
- **Permissions** — admin/member/creator roles enforced server-side.
- **Storage** — file bodies live in Telegram, not on disk.
- **History** — `MessagesGetHistory` is our event log.
- **Identity** — every message carries the sender's user ID.

What Telegram doesn't provide:

- **Folders.** Telegram has no concept. We invent it client-side.
- **Strict immutability.** Members can edit/delete their own messages from the
  regular Telegram app. Convergence relies on members not tampering with TDrive
  messages. This is the single biggest assumption; documented in README.
- **CAS / locks.** No way to atomically reserve a name. We accept last-writer-
  wins for structural conflicts.

### The trick: structure travels in the messages

Every TDrive-meaningful message starts with a machine-readable header. Folder
creates / renames / moves / deletes are sent as text-only "control messages"
with the same header format. Every client downloads channel history, parses
headers, replays them in `msg_id` order. Everyone arrives at identical state.
Telegram is the source of truth; SQLite is a deterministic projection of it.

---

## 3. Wire Format (`TDX1`)

Single header line at the start of the message text. Anything after a `\n` is
human-readable filler. Versioned prefix so future formats degrade gracefully.

### Object IDs are namespaced

- Files: `f:<telegram_msg_id>` — the upload message's own msg_id is the file's
  identity. Aligns with existing `app.go` code which already keys off msg_id.
- Folders: `d:<uuid>` — generated client-side at mkdir time.
- Root parent: `""` (empty string). Frozen everywhere — wire, DB, frontend.
  Never `NULL`, never `"root"`, never `"/"`.

### Op types

```
TDX1|t=f|p=<parent>|n=<urlencoded name>           # file upload (caption)
TDX1|t=mkdir|obj=d:<uuid>|p=<parent>|n=<name>     # create folder
TDX1|t=rename|obj=<f:|d:>|n=<new name>            # rename file or folder
TDX1|t=move|obj=<f:|d:>|p=<new parent>            # move file or folder
TDX1|t=rmdir|obj=d:<uuid>                         # delete folder
TDX1|t=tomb|obj=f:<msg_id>                        # delete file (visibility)
TDX1|t=meta|obj=f:<msg_id>|p=<parent>|n=<name>    # backfill metadata for
                                                  # pre-format upload
```

`f` op has no `obj=` because the file's identity is the message's own msg_id.
Every other op carries an explicit `obj=`.

### Operation identity vs object identity

- `obj_id` — stable identity of the thing acted upon.
- `op_id` — Telegram `msg_id` of the op message itself, implicit. Used as the
  dedupe key for replay (`PRIMARY KEY (channel_id, msg_id)` in `replay_log`).

Two renames of the same folder = two msg_ids, both stored, last wins on apply.

### Control messages are sent silently

`Silent: true` on every control-message send so they don't ping members in the
regular Telegram app. File uploads stay non-silent.

---

## 4. SQLite Schema (post-migration)

```sql
CREATE TABLE channels (
  channel_id             INTEGER PRIMARY KEY,
  access_hash            INTEGER NOT NULL,
  title                  TEXT NOT NULL,
  kind                   TEXT NOT NULL,           -- 'personal' | 'shared'
  invite_link            TEXT,                    -- cache, never trusted
  joined_at              INTEGER NOT NULL,
  last_synced_msg        INTEGER NOT NULL DEFAULT 0,
  last_viewed_msg        INTEGER NOT NULL DEFAULT 0,
  has_unseen_content     INTEGER NOT NULL DEFAULT 0,
  initial_sync_done      INTEGER NOT NULL DEFAULT 0,
  personal_backfill_done INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE replay_log (
  channel_id      INTEGER NOT NULL,
  msg_id          INTEGER NOT NULL,
  op_type         TEXT NOT NULL,
  op_payload_json TEXT NOT NULL,
  raw_header      TEXT NOT NULL,
  first_seen_hash TEXT NOT NULL,                 -- sha256(raw_header)
  actor_user_id   INTEGER NOT NULL DEFAULT 0,
  seen_at         INTEGER NOT NULL,
  PRIMARY KEY (channel_id, msg_id)
);
CREATE INDEX idx_replay_log_channel_msg ON replay_log(channel_id, msg_id);

CREATE TABLE replay_log_tamper (
  channel_id  INTEGER NOT NULL,
  msg_id      INTEGER NOT NULL,
  old_hash    TEXT NOT NULL,
  new_hash    TEXT NOT NULL,
  detected_at INTEGER NOT NULL,
  PRIMARY KEY (channel_id, msg_id)
);

CREATE TABLE backfill_progress (
  channel_id    INTEGER PRIMARY KEY,
  cursor_obj_id TEXT NOT NULL,
  cursor_kind   TEXT NOT NULL,                   -- 'folder' | 'file'
  started_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);

-- Folders: existing table + new columns. ids prefixed "d:<uuid>".
-- channel_id default = personal channel id (resolved at migration time and
-- baked into the DDL the migration runs) so legacy writes during the
-- step-1/step-2 transition still land in the right scope.
ALTER TABLE folders ADD COLUMN channel_id INTEGER NOT NULL DEFAULT <personal_channel_id>;
ALTER TABLE folders ADD COLUMN tombstoned INTEGER NOT NULL DEFAULT 0;
-- Migration step prefixes existing folder.id and folder.parent_id with "d:"
-- (root stays "").
-- Note: no op_uuid column. Op identity lives in replay_log as
-- (channel_id, msg_id). Folder rows are pure projection state.

-- Files: composite PK. SQLite can't alter PK in place, so we rebuild.
CREATE TABLE files_new (
  -- channel_id default = personal channel id (baked in at migration time) so
  -- any legacy INSERT that doesn't yet supply channel_id during the
  -- step-1/step-2 transition lands in the personal scope, not channel 0.
  channel_id       INTEGER NOT NULL DEFAULT <personal_channel_id>,
  msg_id           INTEGER NOT NULL,
  name             TEXT NOT NULL,
  size             INTEGER NOT NULL,
  parent_id        TEXT NOT NULL DEFAULT '',     -- "" or "d:<uuid>"
  upload_time      INTEGER NOT NULL,
  uploader_user_id INTEGER NOT NULL DEFAULT 0,
  tombstoned       INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (channel_id, msg_id)
);
-- migration copies old rows into files_new with channel_id = personal channel,
-- prefixes parent_id with "d:" if non-empty, drops old files, renames.

CREATE INDEX idx_files_channel_parent
  ON files(channel_id, parent_id) WHERE tombstoned = 0;
CREATE INDEX idx_folders_channel_parent
  ON folders(channel_id, parent_id) WHERE tombstoned = 0;
```

`config.json` keeps the personal channel ID only as the "default active drive"
pointer. All membership lives in the `channels` table.

---

## 5. Projection Discipline (the hard rule)

**`files` and `folders` are derived state.** They are never written by anything
except the projector. The projector is the only writer. Enforced by Go package
boundaries:

- Tables defined and written inside `backend/projection/`.
- Read functions exported (`ListFolders`, `ListFiles`, `GetFile`, …).
- Write functions package-private. The rest of the app cannot import them.

### Single apply path

```
user action
  → build TDX op
  → send Telegram message (control or upload)
  → on send-success, in ONE SQLite transaction:
      INSERT INTO replay_log (..., raw_header, first_seen_hash, actor_user_id)
      applyOp(tx, channel_id, msg_id, parsed_op)
      COMMIT
  → UI re-renders from files/folders
```

The same `applyOp` function is called by:
- The local send-success handler (using the msg_id Telegram returns).
- The remote sync engine (using msg_id from history pages).

No "fast path for my own ops." If both paths existed, drift is inevitable.

### Optimistic UI

Folder ops feel laggy if we wait for the round-trip. So:
- On user action, we render an "in-flight" projection in memory only.
- On send-success, we run the real `applyOp`; the in-flight overlay is dropped.
- On send-failure, we drop the overlay and toast an error.

In-flight overlay never touches `files`/`folders`/`replay_log`. It is purely
a UI hint living in frontend state.

### Projector invariants (FROZEN)

```
1. Root parent is "". Never NULL, never "root".
2. Ops apply in strictly ascending (channel_id, msg_id). Sync sorts before
   calling applyOp.
3. Object IDs are prefixed: "f:<msg_id>" or "d:<uuid>". Parents carry the
   prefix. "" is the only unprefixed value (root).
4. mkdir into missing/tombstoned parent: folder is created anyway and
   surfaces in the Orphaned virtual bucket. We never reject a recorded op.
5. move/rename targeting a tombstoned/missing object: ignored, logged.
6. A move that creates a cycle (folder into its own descendant) is rejected
   deterministically by walking ancestors before applying. The replay_log
   row stays; the projection is not mutated. Marker recorded.
7. tomb/rmdir is idempotent. Re-applying does nothing.
8. Virtual buckets (Unmanaged, Orphaned) are SELECT views. Never folder rows.
9. Projector is the ONLY writer to files and folders.
10. applyOp is deterministic. No clocks, no randomness, no external lookups.
```

Tests in `apply_test.go` assert each invariant.

### Tamper detection

On every replay_log insert, store `raw_header` and `first_seen_hash =
sha256(raw_header)`. If sync re-encounters a msg_id that already exists with a
different hash, that's an edited caption: log to `replay_log_tamper`, ignore
the new content, keep the original op. Existing members stay consistent;
fresh installers may see edited state. README states the assumption.

### `rebuildProjection(channel_id)` — first-class debug primitive

```
BEGIN
DELETE FROM files   WHERE channel_id = ?
DELETE FROM folders WHERE channel_id = ?
for each row in replay_log WHERE channel_id = ? ORDER BY msg_id ASC:
  applyOp(tx, channel_id, row.msg_id, parse(row.op_payload_json))
COMMIT
```

Exposed via hidden Wails method `RebuildProjection(channelID)` for debugging.
Also the harness used by replay tests.

---

## 6. Sync Engine

Per-channel, per-direction state machine.

### Phase A — Initial sync (joining a drive, or first run on personal)

1. Mark `initial_sync_done = 0`.
2. Page through history backwards using `MessagesGetHistory` with `OffsetID`,
   1000 per page. Don't download file bodies — metadata only.
3. For each message, parse `TDX1|...`. If absent, the message represents an
   "unmanaged" upload (a document posted via regular Telegram); record it with
   a virtual marker. If present, hash + insert into `replay_log`.
4. After the full traversal, run `applyOp` over the freshly inserted rows in
   ascending `msg_id` order. (Or just call `rebuildProjection`.)
5. Set `last_synced_msg` to the highest msg_id seen, `initial_sync_done = 1`.
6. UI shows progress: "Syncing Goa Trip — 1,243 / 8,500 messages".

### Phase B — Incremental sync

1. `MessagesGetHistory` with `MinID = last_synced_msg`, paginate forward.
2. For each new message: parse, hash, insert into `replay_log` (or detect
   tamper), call `applyOp`.
3. Bump `last_synced_msg`.

### When sync runs

- On channel switch (debounced; skip if synced <30s ago).
- On manual Refresh button.
- On app focus / launch (background, all drives in parallel, capped at 3).
- v2: subscribe to Telegram `updates.GetDifference` for push.

### Concurrency

- One `sync.Mutex` per channel; two syncs of the same drive serialize.
- Different drives run in parallel, capped at 3 concurrent.
- Flood-wait (`FLOOD_WAIT_X`) → sleep X seconds, retry. Surfaced in sidebar:
  "Goa Trip — slowed by Telegram, retrying in 12s".

---

## 7. Joining & Authority

### Creating a shared drive

1. User clicks "+ New shared drive" in sidebar, enters title.
2. Backend calls `ChannelsCreateChannel{Megagroup: true}`.
3. Backend calls `MessagesExportChatInvite` → invite link.
4. Insert row into `channels` (kind = 'shared', `personal_backfill_done = 1`
   since there's no existing local structure to backfill).
5. UI shows a copy-link modal. User sends the link via WhatsApp/iMessage/etc.

### Joining via invite

1. User pastes `t.me/+abc123xyz` (or `joinchat/abc123xyz`).
2. Backend extracts the hash, calls `MessagesImportChatInvite`.
3. On success, insert row into `channels`, switch active drive to it, kick off
   Phase A sync with progress UI.

### Authority

There is nothing for TDrive to authorize. The user already authorized TDrive
to use their Telegram account at first login. That same session can join,
post, read. Joining a shared drive triggers no new permission prompt. If
Telegram lets the join succeed, you're in. If revoked or kicked, Telegram
refuses, and TDrive sees the channel disappear on next sync.

### Roles map to Telegram

- Member → read + upload + delete own files.
- Admin → above + delete others' files + kick + manage invite link.
- Creator → admin + delete drive entirely.

TDrive surfaces these as buttons. No app-level role table.

### Invite link policy

Live `MessagesExportChatInvite` call on every "Copy link" click. The cached
`invite_link` in `channels` is for in-session display only; never durable
truth. Invite-link UI is **admin-only** in v1; non-admin members ask an admin
out-of-band.

---

## 8. Deletion Contract

Tomb is the authoritative convergence signal. Body deletion is best-effort.

### File delete (in shared drive)

1. Send `TDX1|t=tomb|obj=f:<msg_id>` control message **first**.
2. On success, send `messages.deleteMessages` for the file body.
3. If body delete fails, retry up to 3x with backoff. Log if all fail. The
   file is already hidden everywhere (tomb did the work); admins can clean up
   storage later.
4. If tomb send itself fails, surface error and abort. No partial state.

### Folder delete

1. Send `TDX1|t=rmdir|obj=d:<uuid>`.
2. Files inside become orphans (their parent_id still references the
   tombstoned folder; the projector surfaces them in the Orphaned virtual
   bucket). They are NOT auto-deleted.
3. Folder row keeps `tombstoned = 1`; never hard-removed (deterministic
   debugging, easier orphan resolution).

### Personal drive delete

Same code path as shared (tomb-first), but since you're the only member the
tomb is overkill. Acceptable; keeps one path. Body delete still happens.

### "Delete for everyone" wording

Confirm dialog in shared drives says: **"Delete for everyone in Goa Trip?"**
Personal drive uses plain "Delete?".

### Delete by non-owner non-admin

Telegram will reject the body delete for messages you didn't send unless you're
admin. We pre-check admin status before showing the delete option on others'
files. If the API rejects anyway (e.g., admin status changed), we surface the
error; tomb is **not** sent in that case (no point hiding what we can't delete
locally for others).

### File missing in Telegram

If a file message gets deleted via regular Telegram (no tomb), our cache row
stays. UI shows a "missing in Telegram" badge. Download fails gracefully.
We do NOT auto-tombstone on missing-message — that lets one bad actor erase
everyone's view by mass-deleting.

---

## 9. Conflict Resolution

All conflicts resolve by `msg_id` order — the canonical event log of Telegram.

| Conflict | Resolution |
|---|---|
| Two uploads, same name, same folder | Both files exist. Distinct msg_ids. |
| Two `mkdir` same name same parent | Both folders exist. UI may auto-suffix the second. |
| Two renames of same target | Higher `msg_id` wins. |
| Move into rmdir'd folder | Move applies; file orphans. |
| Rename of tombstoned target | Ignored. |
| Move creating cycle | Rejected by ancestor walk; replay_log row keeps a `rejected_cycle` marker; projection unchanged. |
| Same user on two devices | Each device syncs independently; both converge on the same op stream. |

---

## 10. Migration & Backfill

Step 3 of rollout (separate from schema migration in step 1).

### Why backfill is needed

Existing users have folders and files only in local SQLite. If we promised
"SQLite is just a cache," we must publish their existing structure into the
personal channel so a wipe + resync reconstructs it.

### Backfill flow (idempotent, stable-snapshot)

1. On first online launch after upgrade, if `personal_backfill_done = 0`:
2. Pre-scan recent personal-channel history once for any TDX messages already
   posted (defensive against a botched prior backfill).
3. In one tx, copy current `folders` and `files` rows into
   `backfill_snapshot_folders` and `backfill_snapshot_files` (frozen view).
4. Walk snapshot folders parents-first. For each: send
   `TDX1|t=mkdir|obj=d:<existing uuid>|p=<parent>|n=<name>` (silent), unless
   that obj_id already appeared in the pre-scan.
5. After each successful send, in one tx: `INSERT INTO replay_log`,
   `applyOp`, update `backfill_progress` cursor. Idempotent on `(channel_id,
   msg_id)`.
6. Walk snapshot files. For each: send `TDX1|t=meta|obj=f:<msg_id>|p=<parent>
   |n=<name>` (silent). Same projection update.
7. Set `personal_backfill_done = 1`. Drop snapshot tables.
8. Concurrent user actions during backfill flow through the normal "emit op →
   send → project" path. Worst-case: a folder briefly appears twice; converges
   on next sync via msg_id ordering and idempotent upserts.

`mkdir` and `meta` projector handlers are upserts keyed by `(channel_id,
obj_id)`. Re-running the backfill produces the same end state.

---

## 11. UI / UX

### Sidebar (new module: `frontend/src/modules/sidebar.js`)

```
┌─────────────────────────────┐
│ MY DRIVE                    │  ← personal, pinned top
│                             │
│ SHARED WITH ME              │
│  • Goa Trip       (12) ●    │  ● = unread (new managed/unmanaged file)
│  • Family Photos  (203)     │
│  • Roommates       ⚠         │  ⚠ = sync error / kicked
│                             │
│ + New shared drive          │
│ + Join with link            │
└─────────────────────────────┘
```

Click → `SetActiveChannel` → main pane re-renders from local cache (instant).
Sync runs in background.

### Drive header

- Name, kind icon (lock = personal, people = shared), member count.
- Share button (admins only; live invite link fetch).
- Refresh button (manual sync).
- Manage menu: rename drive (admin), leave drive, delete drive (creator).

### File list

- Shared drives: uploader chip ("Jeet · 2h ago") resolved from `uploader_user_id`
  via a tiny lazy `users` cache. Personal drives: chip hidden.
- Files in Unmanaged or Orphaned virtual buckets render with subdued styling
  and a tooltip explaining their state.
- Missing-in-Telegram files render with a warning icon.

### Modals

- New shared drive: title input → invite-link copy modal.
- Join: paste link → progress overlay during initial sync.
- Confirm delete (shared): explicit "for everyone" wording.

### Personal vs shared: strictly separate

No drag between drives in v1. If we add it later, it's a right-click "Copy
to..." that explicitly does upload-then-delete with a visible progress bar.

### Unread semantics

The dot fires only on **new visible content** since `last_viewed_msg`:
- New `t=f` op (managed file upload) by another user.
- New non-TDX document message (unmanaged file).

`mkdir`, `rename`, `move`, `rmdir`, `tomb` never trigger the dot. Implementation:
each `applyOp` returns whether it was a content event; sync engine OR-aggregates
and bumps `has_unseen_content`.

### Virtual buckets

- **Unmanaged** — non-TDX document uploads in shared drives. Top-level synthetic
  entry, only shown if the underlying query returns rows.
- **Orphaned** — files whose `parent_id` points to a non-existent or
  tombstoned folder. Same treatment.
- Neither has a row in `folders`. Move/rename/breadcrumb code never traverses
  into them.

---

## 12. Edge Cases (full table)

| Case | Handling |
|---|---|
| New member joins drive with 8k files | Phase A sync, paginated, progress bar. Drive browsable as data fills. |
| Two simultaneous uploads, same folder | No conflict; both files, distinct msg_ids. |
| Member edits structure offline | v1 blocks: shows "you're offline" banner. v2: queue. |
| Member kicked | Next sync hits `CHANNEL_PRIVATE`; mark drive disabled; UI shows "Access revoked" + "Remove from sidebar". |
| Channel deleted by admin | Same as kicked. |
| Same Telegram account on two devices | Both maintain independent caches; both converge from the same op stream. |
| Reinstall TDrive | Personal drive auto-recreated from `config.json`. Shared drives gone; user re-joins via link. (v2 heuristic: scan for `TDX1` in recent channel histories.) |
| File uploaded outside TDrive | No `TDX1` header; surfaces in Unmanaged bucket. |
| Caption edited post-hoc | Tamper detection logs it; original op stays canonical for already-synced clients; fresh installers may see edited state. |
| Future-version header (`TDX2`) | Unknown version → ignored, logged. Never crash. |
| Sync interrupted mid-page | `last_synced_msg` only bumps after a page tx commits; resume is clean. |
| Telegram flood-wait | Sleep + retry, surfaced in sidebar. |
| Folder rename collides | Both names allowed; Telegram-style. UI may disambiguate. |
| Two clients post mkdir for same `d:uuid` | Idempotent on `(channel_id, msg_id)` and the projector's upsert. First wins; second is no-op semantically. |
| Two clients tomb the same file | Idempotent; both rows in replay_log; projection idempotent. |
| File missing in Telegram (deleted via TG app, no tomb) | Cache row stays; UI shows "missing" badge; download fails gracefully. |
| Migration crash mid-tx | Single-tx migration → SQLite rolls back; retry on next launch. |
| Backfill crash mid-walk | `backfill_progress` cursor + idempotent upserts → safe resume. |
| User actions during backfill | Flow through normal path; converge via msg_id ordering. |
| Move creating cycle | Rejected by ancestor walk; replay_log row marked `rejected_cycle`. |
| Out-of-order msg_ids in a sync page | Sync sorts ascending before calling applyOp. Invariant 2. |

---

## 13. Files Touched

### Backend (new package: `backend/projection/`)

- `schema.go` — schema DDL + migration runner.
- `types.go` — `Op`, `OpType`, `ParsedOp`, `Folder`, `File`, `ReplayLogRow`.
- `wire.go` — `Parse(raw) (ParsedOp, error)`, `Format(op) string`. Handles
  `f:`/`d:` namespacing, `""` root, unknown op/version → typed errors.
- `apply.go` — `applyOp(tx, channelID, msgID, op, actorID)`. The single writer.
  Invariants block at top.
- `rebuild.go` — `rebuildProjection(ctx, channelID)`.
- `read.go` — `ListFolders`, `ListFiles`, `GetFolder`, `GetFile`,
  `OrphanedFiles`, `UnmanagedFiles`. Only public read API.
- `migration.go` — initial v1→v2 migration, backfill snapshot setup, checkpoint
  table.
- `wire_test.go`, `apply_test.go`, `rebuild_test.go`, `migration_test.go`.

### Backend (new package: `backend/sync/`)

- `engine.go` — per-channel sync state, history pagination, parse + log +
  apply.
- `tamper.go` — hash compare, tamper logging.

### Backend (new file: `backend/auth/group.go`)

- `CreateSharedChannel`, `ExportInviteLink`, `JoinByInvite`, `LeaveChannel`,
  invite-link parsing.

### Backend (modified: `app.go`)

- `App.activeChannelID`, `SetActiveChannel`.
- All channel-scoped methods rewritten to:
  - read active channel,
  - emit op → send → project (via single path),
  - return projection-derived data.
- Bound methods: `ListChannels`, `CreateSharedDrive`, `JoinSharedDrive`,
  `GetInviteLink`, `LeaveSharedDrive`, `SetActiveChannel`, `SyncChannel`,
  `RebuildProjection` (debug).

### Frontend

- `frontend/src/state.js` — `activeChannel`, `channels`, `syncStatus`,
  in-flight overlay map.
- `frontend/src/modules/sidebar.js` *(new)* — drive list, badges, CTAs.
- `frontend/src/modules/channels.js` *(new)* — create/join/leave/share modals.
- `frontend/src/modules/file-list.js` — uploader chips, virtual bucket
  rendering, missing badges.
- `frontend/src/modules/modals/` — `share-drive`, `join-drive`, `new-drive`,
  `delete-shared-confirm`.
- `frontend/src/main.js` — wire new modules into init, handle channel-switch
  events.

---

## 14. Rollout Stages

Each stage is independently shippable and reversible.

### Step 1 — Schema + projection skeleton + active-channel plumbing (merged)

The original split between "schema only" and "active-channel plumbing" wasn't
actually safe: the new `files`/`folders` shape requires `channel_id`, but
legacy `app.go` code still inserts without it. Even with default values
falling back to the personal channel, leaving two writers (legacy direct
INSERTs and the projector) violates the single-writer invariant from day one.

So step 1 does the schema work AND the cutover to the projector in one push,
guarded by the personal-channel default so any straggler write is at least
in-scope.

Contents:

- New `backend/projection/` package; tables defined and writable only here.
- Schema migration in one tx: `replay_log`, `replay_log_tamper`,
  `backfill_progress`, `channels`. Personal channel row inserted from
  `config.json`. `files`/`folders` reshape (composite PK on files, prefixed
  ids on folders, `channel_id` defaulted to personal channel for safety
  during the transition window).
- Projector + invariants implemented; `rebuildProjection` exposed for debug.
- `App.activeChannelID` + `SetActiveChannel` (only personal exists for now).
- Every existing channel-scoped method in `app.go` rewritten to:
  scope by `activeChannelID`, emit op → send → project via the single path.
- Wire format parser/formatter + golden tests.
- Replay tests covering each invariant.
- Migration tests (v1 fixture → v2 schema, all rows in personal channel).
- **No shared-drive UI. No new Telegram channel ops. Personal drive behaves
  identically from the user's perspective.**

### Step 2 — Reserved (folded into Step 1)

(Merged with Step 1 to preserve the single-writer invariant; kept as a
section anchor so subsequent step numbers remain stable in references.)

### Step 3 — Wire format on personal + backfill

- New uploads emit `TDX1|t=f|p=...|n=...` captions.
- Folder ops emit silent control messages.
- One-time personal backfill (mkdir + meta) with stable snapshot, idempotent
  resume, durable checkpoint.
- Sync engine for personal channel runs Phase A first time, Phase B forever.
- Personal drive becomes a "single-member shared drive."

### Step 4 — Create + join shared drives

- `backend/auth/group.go` complete.
- Sidebar UI; drive switching.
- Shared drives work file-only at this stage (no folder sync UX yet — tested
  via files only).
- Create / join / leave / share modals.

### Step 5 — Structural ops on shared drives

- mkdir/rename/move/rmdir/tomb fully working in shared drives via the same
  single-apply path.
- Conflict tests with two clients.
- Cycle rejection.

### Step 6 — Polish

- Uploader chips, member counts, unread dots.
- Virtual buckets (Unmanaged, Orphaned) in UI.
- Missing-in-Telegram badges.

### Step 7 — Edge cases

- Kicked detection.
- Flood-wait UI.
- Tamper logging surfaced for debug.
- Reinstall flow hint.

Stop after step 4 → working "shared file dump." Stop after step 5 → full
shared file system.

---

## 15. Verification

- `go test ./backend/projection/...` — wire round-trip, all invariants, replay
  goldens, migration fixtures.
- `go test ./backend/sync/...` — pagination, tamper detection, flood-wait
  retry.
- Migration check: existing user upgrades; `SELECT COUNT(*) FROM files WHERE
  channel_id != <personal>` returns 0; UI looks identical.
- Two-account E2E:
  - A creates "Goa Trip", uploads 3 photos.
  - B joins via link, sees Phase A sync, ends with 3 photos and uploader chips.
  - B uploads a photo, makes "Day1", moves a photo in. A refreshes; sees same.
  - A renames "Day1"→"Day One" while B renames →"D1"; both converge to higher
    msg_id.
  - A deletes a B-uploaded file (A is admin); B sees tombstone next sync.
  - A kicks B; B's next sync surfaces "Access revoked".
  - B reinstalls; personal drive present, shared gone, prompted to rejoin.
- Drive switching never leaks files: `SELECT DISTINCT channel_id FROM files
  WHERE msg_id IN (visible rows)` returns one value.
- Crash mid-backfill: kill process during step 3 backfill, restart, verify
  resume produces correct end state with no duplicates.
- Tamper test: mutate a TDX caption via the regular Telegram client; verify
  `replay_log_tamper` row appears and existing clients keep original op.

---

## 16. Documented Assumptions (for README)

1. **Don't edit or delete TDrive messages from the regular Telegram app.**
   Convergence relies on the message log being effectively append-only.
   Existing clients are protected by tamper detection; fresh installers may
   see different state.
2. **Opening a TDrive shared drive in regular Telegram shows control-message
   clutter.** They are silent and harmless; use TDrive for the clean view.
3. **Photos posted via regular Telegram appear in the Unmanaged bucket** with
   auto-generated names. To get full quality + filename, upload through TDrive.
4. **No offline ops in v1.** Actions require an online Telegram connection.
5. **Initial sync cost grows with channel history.** v2 will introduce
   snapshots / compaction.
6. **Telegram permissions are the only permissions.** TDrive does not enforce
   any app-level role beyond what Telegram does.

---

## 17. v2 Backlog (not v1)

- `MessageMediaPhoto` first-class ingestion.
- Offline op queue with conflict reconciliation.
- Snapshot / log compaction (`TDX1|t=snapshot`).
- Real-time updates via `updates.GetDifference` subscription.
- Auto-discovery of joined TDrive channels after reinstall (scan recent
  channel histories for `TDX1` markers).
- Cross-drive copy ("Copy to shared drive…").
- Per-message signing (cryptographic anti-tamper) if friends-only assumption
  ever needs to harden.
