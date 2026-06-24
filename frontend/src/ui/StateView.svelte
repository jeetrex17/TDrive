<script lang="ts">
    import type { Snippet } from 'svelte';

    type Tone = 'loading' | 'empty' | 'error';

    type StateViewProps = {
        tone?: Tone;
        title: string;
        body?: string;
        busy?: boolean;
        children?: Snippet;
    };

    let {
        tone = 'empty',
        title,
        body = '',
        busy = false,
        children,
    }: StateViewProps = $props();
</script>

<div
    class="state-view"
    class:is-loading={tone === 'loading'}
    class:is-error={tone === 'error'}
    role={tone === 'error' ? 'alert' : 'status'}
    aria-live={tone === 'error' ? 'assertive' : 'polite'}
    aria-busy={busy ? 'true' : 'false'}
>
    <div class="state-icon" aria-hidden="true">
        {#if tone === 'loading'}
            <span class="state-spinner"></span>
        {:else if tone === 'error'}
            !
        {:else}
            —
        {/if}
    </div>
    <div class="state-copy">
        <div class="state-title">{title}</div>
        {#if body}
            <div class="state-body">{body}</div>
        {/if}
        {#if children}
            <div class="state-actions">
                {@render children()}
            </div>
        {/if}
    </div>
</div>

<style>
    .state-view {
        display: grid;
        grid-template-columns: auto minmax(0, 1fr);
        align-items: center;
        gap: var(--space-3);
        color: var(--text-main);
    }

    .state-icon {
        width: 32px;
        height: 32px;
        border: 1px solid var(--border);
        border-radius: var(--radius-full);
        display: grid;
        place-items: center;
        color: var(--text-muted);
        background: var(--surface-control);
        font-size: var(--font-size-sm);
        font-weight: 800;
    }

    .state-spinner {
        width: 14px;
        height: 14px;
        border-radius: var(--radius-full);
        border: 2px solid var(--border-strong);
        border-top-color: var(--accent);
        animation: state-spin 720ms linear infinite;
    }

    .state-copy {
        min-width: 0;
    }

    .state-title {
        color: var(--text-main);
        font-size: var(--font-size-sm);
        font-weight: 800;
        line-height: 1.35;
    }

    .state-body {
        margin-top: 2px;
        color: var(--text-muted);
        font-size: var(--font-size-xs);
        line-height: 1.45;
    }

    .state-actions {
        display: flex;
        flex-wrap: wrap;
        gap: var(--space-2);
        margin-top: var(--space-3);
    }

    .is-error .state-icon {
        color: var(--danger);
        border-color: color-mix(in srgb, var(--danger) 42%, transparent);
        background: color-mix(in srgb, var(--danger) 12%, transparent);
    }

    @keyframes state-spin {
        to { transform: rotate(360deg); }
    }

    @media (prefers-reduced-motion: reduce) {
        .state-spinner {
            animation: none;
        }
    }
</style>
