# Svelte UI foundation

TDrive is a hybrid application: Svelte owns the application root, desktop and
mobile shells, authentication screens and feature surfaces. TypeScript modules
continue to coordinate navigation, backend calls, transfers and media lifecycle.
This directory contains both reusable controls and those composed surfaces.

## Ownership and lifecycle

[`main.ts`](../main.ts) builds the application lifecycle and
[`modules/app-shell.ts`](../modules/app-shell.ts) mounts
[`AppRoot`](app/AppRoot.svelte). The root renders startup/auth/loading/dashboard
states and chooses its desktop or mobile shell after gateway readiness makes the
platform available. It keeps that shell choice for the session.

[`FeatureLayer`](app/FeatureLayer.svelte) renders shared overlays and wires
controller activations to dashboard visibility. Its effect cleanup releases
feature subscriptions and listeners. Keep backend orchestration in typed modules
and adapters; a shell may compose those controllers, while reusable controls
receive data and callbacks. Components must not import generated Wails bindings
directly: use the public [API boundary](../api.ts).

For imperative mounts, [`mountSvelte`](mount.ts) returns the component instance
and an asynchronous, idempotent `destroy()` handle. Retain that handle and call
it when the owning surface is removed. Intro and outro transitions are disabled
by default; pass the explicit options when a transition should run. Controller
activations should likewise return disposers so remounting does not duplicate
listeners or retain stale drive state.

Playback engines, native player geometry and transport lifetime remain in the
TypeScript video controller. Svelte owns the rendered controls and shell. The
[media architecture](../../../docs/architecture/media-streaming.md) describes the transport
and platform boundaries in more detail.

## Shared controls and visual rules

- [`StateView`](StateView.svelte) supplies loading, empty and error copy blocks.
- [`Button`](Button.svelte) and [`IconButton`](IconButton.svelte) supply shared
  controls; icon-only actions still need an accessible label.
- [`ProgressBar`](ProgressBar.svelte) handles bounded and indeterminate progress.
- [`ModalShell`](modals/ModalShell.svelte) and the modal accessibility helpers
  provide the common dialog surface. Reuse their focus and dismissal behavior.
- Use tokens from [`style.css`](../style.css) for colors, spacing, shadows and
  stacking instead of adding separate scales inside components.
- UI glyphs come from Lucide. The [icon contract](icon-system.test.ts) permits
  raw SVG only at the existing video skip controls; a new handwritten glyph
  requires an explicit change to that contract. Product image assets are separate
  from this glyph rule.

Migration work should preserve typed stores and controller ownership while
replacing remaining repeated DOM rendering. Do not introduce a second application
root or duplicate a feature's listeners inside its new Svelte surface.

## Existing verification

[Primitive checks](primitives.test.ts), [application shell tests](app-shell.dom.test.ts)
and adjacent component DOM tests cover mount behavior and interaction contracts.
Use a focused behavior test when changing ownership, cleanup, focus or user
interactions; a successful component compile alone does not verify those flows.

The [gallery performance contract](gallery/README.md) documents the bounded
full-record working set, separate growing anchor/month metadata, virtualization,
image ownership and cross-platform regression checks.
