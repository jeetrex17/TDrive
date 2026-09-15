<script lang="ts">
    interface Props {
        label?: string;
        compact?: boolean;
    }

    const driveRows = [72, 88, 64, 82];
    const fileRows = [74, 56, 68, 48, 62, 71, 53];

    let { label = 'Loading TDrive', compact = false }: Props = $props();
</script>

<section class:compact class="app-skeleton" role="status" aria-busy="true" aria-live="polite">
    {#if compact}
        <div class="compact-content">
            <div class="compact-brand" aria-hidden="true">
                <span class="compact-mark">T</span>
                <span class="compact-wordmark">TDrive</span>
            </div>
            <div class="compact-copy">
                <strong>{label}</strong>
                <span>Syncing your secure workspace</span>
            </div>
            <span class="loading-orbit" aria-hidden="true"></span>
        </div>
    {:else}
        <aside class="skeleton-sidebar" aria-hidden="true">
            <div class="skeleton-logo">TDrive</div>
            <div class="skeleton-nav">
                <span class="skeleton-section skeleton-block"></span>
                {#each driveRows.slice(0, 3) as width, index (width)}
                    <div class="skeleton-drive" style={`--skeleton-delay: ${index * 70}ms`}>
                        <span class="skeleton-drive-icon skeleton-block"></span>
                        <span class="skeleton-drive-name skeleton-block" style={`--skeleton-width: ${width}%`}></span>
                    </div>
                {/each}
                <span class="skeleton-section skeleton-block"></span>
                <div class="skeleton-drive" style="--skeleton-delay: 210ms">
                    <span class="skeleton-drive-icon skeleton-block"></span>
                    <span class="skeleton-drive-name skeleton-block" style={`--skeleton-width: ${driveRows[3]}%`}></span>
                </div>
            </div>
            <div class="startup-status">
                <span class="status-pulse"></span>
                <span>{label}</span>
            </div>
        </aside>

        <div class="skeleton-main" aria-hidden="true">
            <div class="skeleton-toolbar">
                <span class="skeleton-search skeleton-block"></span>
                <div class="skeleton-actions">
                    <span class="skeleton-action skeleton-block"></span>
                    <span class="skeleton-action skeleton-block"></span>
                    <span class="skeleton-avatar skeleton-block"></span>
                </div>
            </div>
            <div class="skeleton-breadcrumb">
                <span class="skeleton-back skeleton-block"></span>
                <span class="skeleton-crumb skeleton-block"></span>
            </div>
            <div class="skeleton-file-head">
                <span class="skeleton-block"></span>
                <span class="skeleton-block"></span>
                <span class="skeleton-block"></span>
                <span class="skeleton-block"></span>
            </div>
            <div class="skeleton-files">
                {#each fileRows as width, index (width)}
                    <div class="skeleton-file-row" style={`--skeleton-delay: ${index * 65}ms`}>
                        <div class="skeleton-file-name">
                            <span class="skeleton-file-icon skeleton-block"></span>
                            <span class="skeleton-name-bar skeleton-block" style={`--skeleton-width: ${width}%`}></span>
                        </div>
                        <span class="skeleton-meta skeleton-block"></span>
                        <span class="skeleton-meta skeleton-block"></span>
                        <span class="skeleton-more skeleton-block"></span>
                    </div>
                {/each}
            </div>
        </div>
    {/if}
</section>

<style>
    .app-skeleton {
        --skeleton-base: var(--surface-control);
        --skeleton-glint: var(--bg-panel);
        display: grid;
        grid-template-columns: 240px minmax(0, 1fr);
        width: 100%;
        height: 100dvh;
        min-width: 0;
        overflow: hidden;
        color: var(--text-main);
        background: var(--bg-dark);
    }

    .skeleton-sidebar {
        display: flex;
        min-height: 0;
        flex-direction: column;
        padding: 20px;
        border-right: 1px solid var(--color-surface-1);
        background: var(--bg-sidebar);
    }

    .skeleton-logo {
        margin-bottom: 40px;
        color: var(--color-text);
        font-size: 1.2rem;
        font-weight: 800;
        letter-spacing: -0.5px;
    }

    .skeleton-nav { flex: 1; }
    .skeleton-section {
        display: block;
        width: 42%;
        height: 8px;
        margin: 0 12px 13px;
    }
    .skeleton-section:not(:first-child) { margin-top: 31px; }

    .skeleton-drive {
        display: grid;
        grid-template-columns: 18px minmax(0, 1fr);
        align-items: center;
        gap: 12px;
        min-height: 36px;
        padding: 0 12px;
    }
    .skeleton-drive-icon { width: 18px; height: 18px; border-radius: var(--radius-sm); }
    .skeleton-drive-name { width: var(--skeleton-width); height: 10px; }

    .startup-status {
        display: flex;
        align-items: center;
        gap: 9px;
        min-height: 42px;
        padding-top: 20px;
        border-top: 1px solid var(--border);
        color: var(--text-muted);
        font-size: var(--type-xs);
        font-weight: var(--weight-semibold);
    }
    .status-pulse {
        width: 7px;
        height: 7px;
        border-radius: 50%;
        background: var(--accent);
        box-shadow: 0 0 0 5px var(--overlay-accent-1);
        animation: status-breathe 1.8s cubic-bezier(0.77, 0, 0.175, 1) infinite;
    }

    .skeleton-main { min-width: 0; background: var(--bg-dark); }
    .skeleton-toolbar {
        display: flex;
        min-height: 70px;
        align-items: center;
        justify-content: space-between;
        gap: var(--space-4);
        padding: 0 var(--space-6);
        border-bottom: 1px solid var(--border);
    }
    .skeleton-search { width: min(300px, 50%); height: 40px; border-radius: var(--radius-md); }
    .skeleton-actions { display: flex; align-items: center; gap: var(--space-3); }
    .skeleton-action { width: 34px; height: 34px; border-radius: var(--radius-md); }
    .skeleton-avatar { width: 32px; height: 32px; border-radius: 50%; }

    .skeleton-breadcrumb {
        display: flex;
        min-height: 48px;
        align-items: center;
        gap: 10px;
        padding: 0 var(--space-6);
        border-bottom: 1px solid var(--border);
        background: color-mix(in srgb, var(--color-surface-0) 58%, transparent);
    }
    .skeleton-back { width: 24px; height: 24px; border-radius: var(--radius-sm); }
    .skeleton-crumb { width: 112px; height: 10px; }

    .skeleton-file-head,
    .skeleton-file-row {
        display: grid;
        grid-template-columns: minmax(0, 2fr) minmax(96px, 1fr) minmax(84px, 1fr) minmax(100px, auto);
        align-items: center;
    }
    .skeleton-file-head {
        min-height: 45px;
        gap: var(--space-5);
        padding: 0 30px;
        border-bottom: 1px solid var(--border);
    }
    .skeleton-file-head > span { width: 48px; height: 8px; }
    .skeleton-file-head > span:last-child { justify-self: end; }
    .skeleton-files { padding: 0 10px; }
    .skeleton-file-row {
        min-height: 52px;
        gap: var(--space-5);
        padding: 0 20px;
        border-bottom: 1px solid var(--border);
    }
    .skeleton-file-name {
        display: grid;
        grid-template-columns: 30px minmax(0, 1fr);
        align-items: center;
        gap: 12px;
        min-width: 0;
    }
    .skeleton-file-icon { width: 30px; height: 30px; border-radius: var(--radius-md); }
    .skeleton-name-bar { width: min(100%, var(--skeleton-width)); height: 11px; }
    .skeleton-meta { width: 58%; height: 9px; }
    .skeleton-more { width: 28px; height: 8px; justify-self: end; }

    .skeleton-block {
        display: block;
        border-radius: var(--radius-pill);
        background-color: var(--skeleton-base);
        background-image: linear-gradient(100deg, transparent 25%, var(--skeleton-glint) 48%, transparent 70%);
        background-size: 240% 100%;
        animation: skeleton-shimmer 1.65s linear var(--skeleton-delay, 0ms) infinite;
    }

    .compact {
        display: block;
        width: min(390px, 100%);
        height: auto;
        border: 1px solid var(--border);
        border-radius: var(--radius-xl);
        background: var(--bg-sidebar);
        box-shadow: var(--shadow-lg);
    }
    .compact-content {
        display: grid;
        grid-template-columns: auto minmax(0, 1fr) auto;
        align-items: center;
        gap: var(--space-4);
        padding: 18px 20px;
    }
    .compact-brand { display: grid; justify-items: center; gap: 3px; }
    .compact-mark {
        display: grid;
        width: 30px;
        height: 30px;
        place-items: center;
        border-radius: var(--radius-md);
        color: var(--color-on-accent);
        background: var(--accent);
        font-size: var(--type-sm);
        font-weight: 900;
    }
    .compact-wordmark { color: var(--color-text); font-size: 0.62rem; font-weight: 800; letter-spacing: -0.2px; }
    .compact-copy { display: grid; gap: 3px; min-width: 0; }
    .compact-copy strong { overflow: hidden; color: var(--color-text); font-size: var(--type-base); text-overflow: ellipsis; white-space: nowrap; }
    .compact-copy span { color: var(--text-muted); font-size: var(--type-xs); }
    .loading-orbit {
        width: 22px;
        height: 22px;
        border: 2px solid var(--overlay-accent-3);
        border-right-color: var(--accent);
        border-radius: 50%;
        animation: status-spin 0.9s linear infinite;
    }

    @keyframes skeleton-shimmer { from { background-position: 200% 0; } to { background-position: -40% 0; } }
    @keyframes status-breathe { 50% { opacity: 0.48; transform: scale(0.82); } }
    @keyframes status-spin { to { transform: rotate(360deg); } }

    @media (max-width: 720px) {
        .app-skeleton:not(.compact) { grid-template-columns: 1fr; grid-template-rows: 72px minmax(0, 1fr); }
        .skeleton-sidebar { flex-direction: row; align-items: center; justify-content: space-between; padding: 0 20px; border-right: 0; border-bottom: 1px solid var(--border); }
        .skeleton-logo { margin: 0; }
        .skeleton-nav { display: none; }
        .startup-status { min-height: 0; padding: 0; border: 0; }
        .skeleton-toolbar, .skeleton-breadcrumb { padding-inline: var(--space-4); }
        .skeleton-file-head, .skeleton-file-row { grid-template-columns: minmax(0, 2fr) minmax(76px, 1fr); }
        .skeleton-file-head > :nth-child(n + 3), .skeleton-file-row > :nth-child(n + 3) { display: none; }
        .skeleton-file-head { padding-inline: 26px; }
        .skeleton-file-row { padding-inline: 16px; }
        .app-skeleton.compact { display: block; width: min(390px, 100%); height: auto; }
    }

    @media (max-width: 440px) {
        .skeleton-search { width: min(220px, 68%); }
        .skeleton-action:first-child { display: none; }
        .compact-content { grid-template-columns: auto minmax(0, 1fr); }
        .loading-orbit { display: none; }
    }

    @media (prefers-reduced-motion: reduce) {
        .skeleton-block, .status-pulse, .loading-orbit { animation: none; }
    }
</style>
