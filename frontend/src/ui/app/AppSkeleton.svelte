<script lang="ts">
    interface Props {
        label?: string;
        compact?: boolean;
    }

    let { label = 'Loading TDrive', compact = false }: Props = $props();
</script>

<section class:compact class="app-skeleton" aria-busy="true" aria-label={label}>
    <span class="sr-only">{label}</span>
    <div class="skeleton-sidebar" aria-hidden="true">
        <span class="skeleton-logo skeleton-block"></span>
        <span class="skeleton-section skeleton-block"></span>
        {#each [72, 88, 64] as width (width)}
            <span class="skeleton-drive skeleton-block" style={`--skeleton-width: ${width}%`}></span>
        {/each}
        <span class="skeleton-section skeleton-block"></span>
        {#each [82, 58] as width (width)}
            <span class="skeleton-drive skeleton-block" style={`--skeleton-width: ${width}%`}></span>
        {/each}
    </div>
    <div class="skeleton-main" aria-hidden="true">
        <div class="skeleton-toolbar">
            <span class="skeleton-search skeleton-block"></span>
            <span class="skeleton-avatar skeleton-block"></span>
        </div>
        <span class="skeleton-breadcrumb skeleton-block"></span>
        <div class="skeleton-file-head">
            <span class="skeleton-block"></span><span class="skeleton-block"></span><span class="skeleton-block"></span>
        </div>
        <div class="skeleton-files">
            {#each [100, 84, 92, 66, 76, 88] as width (width)}
                <div class="skeleton-file-row">
                    <span class="skeleton-file-icon skeleton-block"></span>
                    <span class="skeleton-file-name skeleton-block" style={`--skeleton-width: ${width}%`}></span>
                    <span class="skeleton-meta skeleton-block"></span>
                    <span class="skeleton-meta skeleton-block"></span>
                </div>
            {/each}
        </div>
    </div>
</section>

<style>
    .app-skeleton {
        display: grid;
        grid-template-columns: minmax(190px, 248px) minmax(0, 1fr);
        width: min(1120px, 100%);
        min-height: min(680px, calc(100dvh - var(--space-10)));
        overflow: hidden;
        border: 1px solid var(--border);
        border-radius: var(--radius-lg);
        background: var(--bg-panel);
        box-shadow: var(--shadow-lg);
    }

    .skeleton-sidebar { padding: var(--space-6) var(--space-4); border-right: 1px solid var(--border); }
    .skeleton-main { min-width: 0; padding: var(--space-5); }
    .skeleton-toolbar, .skeleton-file-row { display: grid; align-items: center; gap: var(--space-3); }
    .skeleton-toolbar { grid-template-columns: minmax(0, 1fr) 32px; }
    .skeleton-logo { width: 76px; height: 18px; margin-bottom: var(--space-8); }
    .skeleton-section { width: 52%; height: 10px; margin: var(--space-5) 0 var(--space-3); }
    .skeleton-drive { display: block; width: var(--skeleton-width); height: 14px; margin: var(--space-4) 0; }
    .skeleton-search { height: 38px; border-radius: var(--radius-md); }
    .skeleton-avatar { width: 32px; height: 32px; border-radius: 50%; }
    .skeleton-breadcrumb { width: 28%; height: 12px; margin: var(--space-7) 0 var(--space-5); }
    .skeleton-file-head { display: grid; grid-template-columns: minmax(0, 1fr) 18% 14%; gap: var(--space-4); padding: 0 var(--space-3) var(--space-3); }
    .skeleton-file-head > span { height: 9px; }
    .skeleton-files { border-top: 1px solid var(--border); }
    .skeleton-file-row { grid-template-columns: 20px minmax(0, 1fr) 18% 14%; min-height: 52px; border-bottom: 1px solid var(--border); padding: 0 var(--space-3); }
    .skeleton-file-icon { width: 18px; height: 18px; border-radius: 5px; }
    .skeleton-file-name { width: min(100%, var(--skeleton-width)); height: 12px; }
    .skeleton-meta { height: 10px; }
    .skeleton-block { display: block; background: linear-gradient(100deg, var(--bg-hover) 25%, var(--overlay-accent-1) 45%, var(--bg-hover) 65%); background-size: 240% 100%; animation: skeleton-shimmer 1.5s ease-in-out infinite; }

    .compact { min-height: 280px; grid-template-columns: 0 minmax(0, 1fr); }
    .compact .skeleton-sidebar { display: none; }

    @keyframes skeleton-shimmer { from { background-position: 200% 0; } to { background-position: -40% 0; } }
    @media (prefers-reduced-motion: reduce) { .skeleton-block { animation: none; } }
    @media (max-width: 640px) { .app-skeleton { grid-template-columns: 1fr; border-radius: var(--radius-md); } .skeleton-sidebar { display: none; } .skeleton-main { padding: var(--space-4); } .skeleton-file-head, .skeleton-file-row { grid-template-columns: 20px minmax(0, 1fr) 22%; } .skeleton-file-head > span:last-child, .skeleton-file-row > :last-child { display: none; } }
</style>
