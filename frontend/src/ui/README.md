# Svelte UI Foundation

TDrive is migrating to Svelte incrementally. Keep this directory focused on
small, reusable UI components that can be mounted from the existing TypeScript
modules while the app is still hybrid.

Rules for this layer:

- Components do not call Wails bindings directly. Put backend access behind
  typed adapters first, then pass data and callbacks into components.
- Prefer design tokens from `style.css`; do not introduce one-off colors,
  shadows, z-index values, or spacing scales inside components.
- Keep the video player as a vanilla TypeScript island until the shared app
  shell and file-list surfaces are stable.
- New components should include a small compile or behavior test when practical.

Current primitives:

- `StateView` for loading, empty, and error copy blocks.
- `Button` and `IconButton` for token-driven controls.
- `ProgressBar` for bounded and indeterminate progress.
- `mountSvelte` for mounting components from existing TypeScript modules with
  explicit teardown.

Migration rule of thumb: replace repeated DOM-string UI first, then move larger
stateful surfaces once their data model has a typed adapter.
