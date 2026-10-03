# TDrive agent guide

TDrive is a Telegram-backed file manager for desktop, Android and iOS. The Wails
GUI and CLI daemon share a Go backend; the frontend combines Svelte components
with existing TypeScript controllers.

Read [CONTRIBUTING.md](CONTRIBUTING.md) before preparing commits or creating or
editing a PR. It defines concise commit messages, purpose-focused PR titles and
the required PR body format.
Use [build/README.md](build/README.md) for platform setup and packaging details.

## Read by task

Read the relevant design note and linked source before changing a subsystem.
The [architecture index](docs/architecture/README.md) links all technical guides.

| Task | Starting point |
| --- | --- |
| App/CLI integration or mobile lifecycle | [Runtime and platforms](docs/architecture/runtime-and-platforms.md) |
| Remote metadata, replay or sync | [Storage and synchronization](docs/architecture/storage-and-sync.md) |
| Upload, retry, mount writes or deletion | [Transfers and mounted writes](docs/architecture/transfers-and-mounts.md) |
| Vault or encrypted reads | [Encryption](docs/architecture/encryption.md) |
| Playback, byte ranges or native player | [Media streaming](docs/architecture/media-streaming.md) |
| Update installation or packaging | [Desktop updates](docs/architecture/desktop-updates.md) |
| Gallery or image caching | [Gallery architecture and performance](frontend/src/ui/gallery/README.md) |
| Photo backup | [Backup lifecycle and ledger](backend/photobackup/README.md) |

## Working scope

- Inspect the current branch, working tree and relevant code before editing.
  Preserve existing user changes and work belonging to other agents.
- Describe behavior implemented in this checkout. Planning documents and
  pending branches are not proof of shipped behavior.
- Follow the requested scope. A request to investigate, review or plan does not
  authorize implementation. A local draft request does not authorize publishing.
- For changes spanning services, persistence or platforms, outline the affected
  boundaries and failure cases before implementation. Keep routine fixes small.
- Follow existing patterns before adding dependencies or abstractions. Extract
  shared behavior when there is a concrete caller or duplicated responsibility.
- If delegating, give each agent a specific task, file ownership and action scope.
  Reviewers stay read-only. Pass along applicable repository instructions.
- Read nested instructions when working in their directories. In particular,
  `launch-video/AGENTS.md` covers the video project, not the application.

## Where code belongs

| Area | Location and responsibility |
| --- | --- |
| App integration | `internal/app/`; Wails services and application lifecycle; root `main.go` embeds assets and starts the app |
| Shared backend | `backend/`; core, domain services, Telegram transport and persistence |
| CLI | `cmd/tdrive/` and `backend/daemon/`; commands and the local protocol |
| Frontend | `frontend/src/`; UI, state and the API adapter in `api.ts` |
| Generated API | `frontend/bindings/`; Wails-generated TypeScript |
| Platform builds | `build/`, `scripts/` and the root `Taskfile.yml` |

## Architecture and data safety

- Follow the domain service split in `internal/app/services.go`. Keep process lifecycle
  ordering in its existing owner; domain services should receive narrow dependencies.
- Reuse `backend/core.Engine` for backend assembly. The GUI and daemon are
  alternative owners guarded by `processlock`, not independent concurrent engines.
- Keep shared business rules in the backend. Frontend calls should use the
  existing API adapter and Wails service boundary.
- Ingest remote operations through `projection.ProjectFromOp` or its transaction
  variant. Preserve replay and watermark atomicity; do not patch derived tables
  to bypass the operation log. Do not hold SQLite transactions across network I/O.
- Preserve account and channel scoping across asynchronous operations, caches
  and persistence. Never let a drive switch redirect an in-flight operation.
- Before changing uploads, retries or cleanup, read the package contract at the
  top of `backend/services/file/service.go` and the relevant caller paths.
  Preserve manifest commit semantics, stable send identifiers and handling of
  uncertain Telegram outcomes. An error alone does not prove a send failed.
- Keep upload limits centralized in `backend/services/file/limits.go`. Splitting
  uses stored size, including encryption overhead. Do not duplicate thresholds
  or infer feature eligibility from the multipart boundary.
- Preserve deletion semantics per surface: file-service delete uses trash;
  mounted WebDAV delete uses the durable hard-delete flow. Cleanup must follow
  persisted ownership/receipt information, never guesses after an uncertain send.
- Treat local journals, staged sources and recovery state as potentially durable
  user data. Inspect their lifecycle before deleting or rebuilding them.
- Preserve encryption formats, key ownership and zeroing behavior. Never log
  credentials, session material, passwords, keys or decrypted user content.
- Keep personal-drive encryption policy separate from the active browsing drive.
  Preserve the mount lifecycle gate and media generation checks during lock/logout.
- Treat media URLs as capabilities. Revoke sessions and image leases on their
  existing lifecycle boundaries; never persist or log access tokens.
- Validate paths, identifiers and permissions at service boundaries. Preserve
  existing authorization and local media-server protections.

## Code and UI conventions

- Use the Go version in `go.mod` and the project's supported APIs. Apply the
  Modern Go Guidelines skill when available for Go changes, and run `gofmt`.
- Keep resource ownership explicit: cancellation, goroutine shutdown, locks,
  file handles and temporary-file cleanup must cover failure paths too.
- Bound concurrency and memory use. Stream large files where practical and
  preserve backpressure; avoid loading whole transfers into memory.
- Preserve useful error context and expose actionable UI errors. Follow existing
  error-handling patterns; do not silently swallow failures.
- Branch on stable operation error codes, not display text. Keep typed runtime
  events and daemon protocol changes consistent with their callers.
- Follow the existing Svelte, TypeScript and styling conventions. Reuse UI
  components and state patterns before introducing parallel implementations.
- Follow [the UI foundation](frontend/src/ui/README.md): components receive data
  and callbacks; controllers own playback/native geometry. Preserve DOM hooks
  still used by TypeScript controllers and release resources on unmount.
- Keep the gallery's full-record working set, DOM and image work bounded by the
  viewport and fixed budgets. Account separately for growing month/anchor metadata;
  do not replace keyset paging or lease ownership with full-library record loads.
- Handle loading, empty, failure, cancellation and recovery states relevant to
  the change. Preserve keyboard access, labels, focus behavior and touch usability.
- Check desktop, Android and iOS implications for shared changes, including
  file access, permissions, app suspension and native bridge behavior.
  Do not promise background execution that the platform cannot provide.

## Commands and generated files

Use the pinned tooling and native dependencies described in `build/README.md`.
Run these commands from the repository root unless another directory is shown.

| Purpose | Command |
| --- | --- |
| Install frontend dependencies | `npm ci --prefix frontend` |
| Develop the application | `wails3 dev` |
| Build for the host desktop | `wails3 task build` |
| Discover platform tasks | `wails3 task -l` |
| Generate Wails bindings | `wails3 task common:generate:bindings` |
| Go tests | `go test . ./internal/... ./cmd/... ./backend/...` |
| Go analysis | `go vet . ./internal/... ./cmd/... ./backend/...` |
| Frontend checks | In `frontend/`: `npm run typecheck`, `npm run lint`, `npm run test:coverage`, `npm run build` |
| Browser contracts | In `frontend/`: `npm run test:e2e` |
| Gallery WebKit contracts | In `frontend/`: `npx playwright test --config playwright.gallery-webkit.config.ts` |

- On Linux, direct Go build/test/vet commands need `-tags=gtk3`; pass
  `BUILD_FLAGS=-tags=gtk3` to the binding-generation task. Native prerequisites
  still apply. Mobile builds use their own tasks in `build/README.md`.
- Root-package Go commands embed `frontend/dist`; on a fresh checkout, build
  the frontend first so the embedded assets exist.
- Regenerate `frontend/bindings/` when exposed Go APIs change; do not hand-edit
  generated bindings. Inspect the generated diff and any module-file changes.
- Keep bound service methods consistent across platforms. Platform-specific
  exposed methods require corresponding binding validation.
- Before regenerating build assets after a `build/config.yml` change, read the
  overwrite notes in `build/README.md` and preserve required platform customizations.
- Update the relevant architecture note when changing its behavior or constraints.
  Keep detailed explanations there and contribution rules in `CONTRIBUTING.md`.

## Verification

- Read [TESTING.md](TESTING.md) before adding or changing tests or choosing
  verification. It defines Go testing conventions, reusable TDrive fixtures,
  subsystem contracts and the limits of each kind of test evidence.
- Start with checks that exercise the changed behavior. Extend existing focused
  tests or table cases where practical; add new files when they improve organization.
- For bug fixes, reproduce the failure and add a regression case when feasible.
  Test observable behavior and meaningful failure paths, not implementation trivia.
- Use `.github/workflows/ci.yml` as the source for broader validation, including
  race checks, generated bindings, browser contracts and platform builds.
  Browser checks require the Playwright browsers and dependencies used by CI.
- Do not weaken checks to pass CI. The frontend lint warning allowance is a
  ratchet: lower it when warnings are removed and never raise it to hide new ones.
- Distinguish checks actually run from those unavailable or still needed.
  Compilation, browser mocks and simulators do not prove physical-device behavior.
- Keep verification results in the work summary. PR bodies follow the separate
  format in `CONTRIBUTING.md`, which omits testing details unless requested.
