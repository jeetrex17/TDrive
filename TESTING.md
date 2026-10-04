# Testing TDrive

Read this guide before adding or changing tests or choosing verification for a
change. Use the relevant [architecture guide](docs/architecture/README.md) for
the subsystem's contract, [build guidance](build/README.md) for dependencies and
[CI workflow](.github/workflows/ci.yml) for required checks.

Every test should answer: **what plausible incorrect behavior would this reject?**
Protect user data, observable behavior and resource limits. Keep tests small
enough that a failure explains the broken contract without debugging a framework.

## Choose the test before writing it

1. State the behavior and failure being protected. For a bug, reproduce it with
   a regression case before changing the implementation when feasible. Confirm
   the failure comes from the bug, then verify the fix and refactor with it green.
2. Search nearby tests and helpers. Extend an existing case or suite when it
   exercises the same contract. Add a file when it gives a distinct responsibility
   a clearer home; do not create a file for every small change.
3. Choose the smallest layer that can expose the failure. Use unit tests for
   decisions, integration tests for interactions and persistence, and browser or
   native checks for behavior that depends on those runtimes. Add coverage at
   multiple layers when each protects a different failure.
4. Define the expected result independently of the implementation being checked.
   Do not calculate the expected offset, retry decision or packet layout with
   the same production helper under test. Round trips are useful, but also need
   independent examples when both directions could share a mistake.
5. Assert consequences: bytes, durable records, visible state, released resources
   or bounded work. Call counts and ordering are appropriate when they protect
   duplicate prevention, commit ordering, cleanup or concurrency limits.

Keep assertions near the action. Name cases after behavior and include useful
input, actual and expected values in failures. Avoid broad snapshots, assertions
that only repeat mock returns, and source-text searches used to claim runtime
correctness. Source checks are appropriate for intentional static contracts such
as generated metadata or the repository's icon policy.

## Go conventions

Use the Go version in [go.mod](go.mod), currently 1.25.13, and follow the Modern Go
Guidelines skill for Go changes. Prefer the standard `testing` package and the
existing package helpers over a new assertion or mocking dependency.

### Fixtures, cleanup and errors

- Mark helpers with `t.Helper()`. Give them concrete inputs and return the values
  the test needs. Keep scenario-specific assertions in the test; avoid helpers
  with many booleans, hidden defaults or callbacks that implement another test DSL.
- Use `t.TempDir()` and `t.Cleanup()` for owned resources. Cleanup runs in reverse
  registration order after subtests finish. A parent's `defer` can close a shared
  fixture before parallel subtests run; use cleanup for that fixture instead.
- Stop and join workers before closing their database, server or files. Restore
  replaced globals and release leases even when an assertion fails. Check cleanup
  errors when they could hide a resource leak or loss of durable state.
- Use `t.Context()` for work tied to the test lifetime. It is canceled before
  cleanup callbacks run. Teardown that needs a live context must create its own
  bounded context; do not reuse an already canceled operation context.
- Use `errors.Is` or `errors.As` for semantic error contracts. Check exact text
  when wording itself is the contract, such as redaction or user-facing copy.
- Test through the owning API where practical. Same-package tests remain useful
  for key clearing, ownership and other private invariants. Do not export a
  production symbol solely to make a trivial assertion possible.

### Tables and isolation

- Use named table cases when setup, execution and assertions have the same shape.
  Keep separate tests when merging them introduces branching setup or unrelated
  assertions. Fold duplicate coverage, not distinct behaviors.
- Add `t.Parallel()` only after checking isolation. Environment changes, working
  directory changes, package caches, native singletons and shared fixtures can
  make parallel tests invalid. `t.Setenv()` cannot run in a parallel test or under
  a parallel ancestor.
- A fresh fixture per test is the default. Sharing a fixture for speed requires
  explicit ownership and protection against state leaking between cases.
- Keep normal tests offline and independent of personal Telegram accounts,
  sessions, local databases and private media. Use synthetic or redistributable
  fixtures; never include real capability URLs, credentials or decrypted user data.

### Goroutines and time

- Report worker errors through a result channel or another synchronized result.
  Call `t.Fatal` and `t.FailNow` from the test goroutine, not a spawned worker.
- Wait for an observable event or completion signal instead of sleeping and
  assuming work finished. Use a bounded timeout to detect hangs; do not use a
  tight wall-clock threshold as a performance assertion.
- Reuse injected clocks and retry sleepers already present in the package.
  Consider `testing/synctest` for timer-driven code whose goroutines and waits fit
  its model. Use the Go 1.25 `synctest.Test` API, not the older experimental API.
- `synctest` does not virtualize filesystem or network I/O or explore all possible
  schedules. Keep real SQLite, HTTP and native integrations outside assumptions
  about virtual time. Advancing time also requires waiting for resulting work
  to settle before inspecting state.
- Exercise cancellation and shutdown while work is blocked when that is the
  changed contract. Avoid asserting global goroutine counts: unrelated runtime
  work can change them. Observe the worker or resource owned by the fixture.

## Reuse TDrive's existing boundaries

### Uploads, downloads and uncertain remote outcomes

Start with [file service fixtures](backend/services/file/service_test.go) and
[upload retry tests](backend/services/file/upload_retry_test.go). `newTestService`
uses real SQLite projection tables and `tgclient.Fake`. Extend this setup before
building another service harness. A fake proves behavior under its model;
protocol assumptions still need independent evidence.

- Inject failures at the actual boundary: before acceptance, after acceptance
  with a lost receipt, or while the remote outcome remains unknown. These states
  require different retry and cleanup decisions.
- Check stable send IDs, accepted remote objects, projected metadata and final
  file bytes. For uncertain manifest outcomes, verify parts remain available for
  reconciliation and are not deleted merely because a call returned an error.
- Use small payloads with injected limits to exercise multipart behavior. Keep
  separate limit cases immediately below, at and above the production threshold,
  including encryption overhead. Do not allocate gigabytes to check size math.
- Treat feature eligibility and multipart splitting as separate contracts. An
  upload being split does not by itself make it eligible for a resume feature.
- On failed downloads, check that an existing destination remains intact and
  that temporary-file handling matches the operation's recovery contract.

### Persistence, sync and recovery

Use [sync fixtures](backend/sync/sync_test.go), the
[single-connection regression](backend/sync/connection_hold_test.go) and
[mount coordinator fixtures](backend/mountwrite/coordinator_test.go).

- Use real SQLite for SQL, migrations, transactions and projection behavior.
  Match the relevant production constraints, including pool size, foreign keys
  or journal mode when the behavior depends on them. In-memory databases are
  suitable for many projection tests, but do not establish restart durability.
- Assert records and watermarks after replay, duplicates, interruption or failure.
  Preserve account/channel isolation and ensure network waits do not hold a
  transaction or exhaust the only database connection needed by other work.
- Distinguish three kinds of recovery evidence: seeding persisted state and
  calling recovery; closing and reopening storage with a fresh owner; terminating
  and restarting a process. A file-backed database alone does not prove the last
  two. Add the level needed by the failure under investigation.
- Check journal transitions, staged bytes and pending cleanup receipts together.
  Repeating recovery should preserve the intended result without duplicate work.
  Use existing failure wrappers to exercise persistence failures at commit points.
- Preserve each surface's recovery policy. Mounted writes can abort unfinished
  mutations; photo backup can pause uncertain work. Do not transfer upload-resume
  expectations into those paths without an intentional product change.

For photo backup, extend [engine tests](backend/photobackup/engine_test.go).
Cancellation after a valid remote receipt must still preserve the authoritative
result. Assert ledger state and subsequent retry behavior, not only the returned
context error. Migration tests should begin with the relevant old schema and
verify retained data as well as the new schema.

### Encryption, ranges and media formats

Use [random-access crypto tests](backend/crypto/random_access_test.go),
[HTTP range tests](backend/media/server_test.go),
[encrypted media tests](backend/media/service_encrypted_test.go) and
[range-window tests](backend/media/range_reader_window_test.go).

- Compare decrypted reads with original plaintext or `bytes.Reader`. Cover the
  relevant chunk edges, partial final chunks, truncation and invalid offsets.
- Distinguish authentication on open from authentication of an interior chunk
  when read. Test wrong keys and tampering where they must be detected. Include
  cancellation, close/read interactions and key ownership when changing lifecycle.
- For HTTP behavior, check exact body bytes and relevant status/range/length
  headers, authorization and cache policy. A successful status alone is inadequate.
- For caching and prefetch, assert bounded reads, coalescing and cancellation of
  stale work after a seek. Use synthetic offset readers for very large positions.
- Keep container parsing, codec support, remux correctness and player decoding
  distinct. Passing a parser test does not establish playable output.

[Matroska tests](backend/media/remux/matroska_test.go) use recorded `ffprobe`
packet data as an independent reference. [Stream tests](backend/media/remux/stream_test.go)
and [HLS tests](backend/media/hls_test.go) exercise real media tools. Preserve fixture
provenance and generation instructions. Investigate changed expected output before
regenerating it; do not make a failing golden match the implementation blindly.
Report media tests skipped for missing tools or codecs as unverified behavior.

### Frontend, gallery and platform integration

Follow the test environments in [vite.config.ts](frontend/vite.config.ts): ordinary
tests for logic/SSR, `*.dom.test.ts` for mounted components in happy-dom, and
[Playwright tests](frontend/e2e) for real browser behavior. SSR and happy-dom do
not establish browser layout, image decoding or video playback.

- Assert user actions and visible outcomes with stable semantic selectors where
  available. Use retrying browser assertions rather than arbitrary delays.
- Reuse [gallery fixtures](frontend/e2e/gallery-fixtures.ts) and the
  [video browser tests](frontend/e2e/video.spec.ts) when actual loaded image bytes
  or media readiness matter. Setting `src` alone does not prove successful loading.
- Check the [gallery contract](frontend/src/ui/gallery/README.md) for bounded
  records, DOM, requests and leases. Exercise stale results after a generation,
  drive or viewport change when modifying asynchronous ownership.
- Configure relevant Wails service responses explicitly and assert their effects.
  The [browser mock](frontend/e2e/wails-mock.ts) rejects unknown or unconfigured
  application calls and fails the journey even if the UI catches that error.
  Shared startup plans and an explicit native-housekeeping allowlist keep boot
  behavior repeatable; configure scenario-specific results rather than adding
  permissive fallbacks.
- Keep wire/version tests in their owning backend package and UI translation
  tests at the adapter or app boundary. Regenerate changed Wails bindings and
  verify callers; do not hand-edit generated code or repeat mock echoes per method.
- Shared changes need desktop, Android and iOS consideration. Native compilation,
  browser emulation, simulator checks and physical-device checks establish
  different things. File access grants, suspension, process death, native playback
  and device memory need the corresponding platform evidence when affected.

## Verification and performance

Start with the affected test or package, then run the relevant integration and
required CI checks. Broaden checks for a concrete risk, cross-package change or
required gate. Use the first-party package list from the current checkout's
[CI workflow](.github/workflows/ci.yml); update that list when packages move.

Run Go examples from the repository root. Commands below select existing tests
and are examples to adapt to the changed behavior, not a checklist for every edit.

| Purpose | Example |
| --- | --- |
| Upload regression | `go test ./backend/services/file -run '^TestMultipartManifestUnknownOutcomeKeepsPartsForReconcile$' -count=1` |
| Affected package | `go test ./backend/services/file` |
| Race check for changed concurrency | `go test -race -count=1 ./backend/mountwrite` |
| Active fuzzing | `go test ./backend/crypto -run '^$' -fuzz '^FuzzRandomAccessDecryptorChunkBoundaries$' -fuzztime=30s` |
| Gallery benchmark | `go test ./backend/projection -run '^$' -bench '^BenchmarkGallery100K$' -benchmem -count=5` |
| Focused frontend file | In `frontend/`: `npm test -- src/ui/gallery/gallery-source.test.ts` |
| Frontend coverage gate | In `frontend/`: `npm run test:coverage` |
| Browser media | In `frontend/`: `npm run test:e2e -- video.spec.ts` |
| Gallery WebKit | In `frontend/`: `npx playwright test --config playwright.gallery-webkit.config.ts` |

- Use [build/README.md](build/README.md) for native prerequisites, Linux's `gtk3`
  build tag and mobile tasks. Build `frontend/dist` before commands that compile
  the root package's embedded assets. Browser checks need installed Playwright
  browsers and their dependencies. Media integration checks may need FFmpeg tools.
- Preserve coverage gates and their intended scope. The frontend currently
  requires 80% branches, functions, lines and statements in selected behavior
  modules. Coverage identifies unexecuted paths; inspect assertions as well.
  Do not lower thresholds, expand exclusions or add trivial tests to manufacture
  a passing percentage. Preserve the lint warning ratchet too.
- CI's race job covers selected packages. Run targeted race checks for changed
  concurrent code outside that selection as needed. The detector only observes
  executed paths; passing it does not prove every schedule safe.
- Normal Go tests run fuzz seeds; mutation-based exploration requires `-fuzz`.
  Keep fuzz inputs and work bounded, deterministic and isolated. Preserve minimal
  regression seeds. Extend existing fuzz targets before creating overlapping ones.
- For performance changes, use existing benchmarks, `b.Loop()` and allocation
  reporting where appropriate. Keep setup outside measurement. Compare repeated
  before/after runs under equivalent conditions; use `benchstat` when available.
  Keep resource-budget assertions in tests and timing comparisons in benchmarks.

## Review before finishing

- Can the test reject a plausible bug, and would a harmless refactor preserve it?
- Is its expected result independent and its fixture representative of the failure?
- Does an existing test already protect the same behavior? Can shared setup be
  reused without hiding the scenario or merging unrelated cases?
- Are the relevant failure, cancellation, isolation and cleanup outcomes checked?
- Does asynchronous work finish before assertions and resource teardown?
- Are claims limited to the layers, platforms and checks actually exercised?

Fix incorrect tests when their contract is wrong; never weaken a correct assertion
to accommodate a regression. Report commands run, failures, skips and remaining
limitations in the work summary. Follow [CONTRIBUTING.md](CONTRIBUTING.md) for PR
bodies, which omit testing details unless requested.

## References

- [Go testing APIs and lifecycle](https://pkg.go.dev/testing)
- [Go asynchronous testing and virtual time](https://go.dev/blog/testing-time)
- [Google Go guidance on table-driven tests](https://google.github.io/styleguide/go/decisions.html#table-driven-tests)
- [Go race detector](https://go.dev/doc/articles/race_detector)
- [Go fuzzing](https://go.dev/doc/security/fuzz/)
- [Playwright testing practices](https://playwright.dev/docs/best-practices)
