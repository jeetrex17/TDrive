<script lang="ts">
    import { onMount } from 'svelte';
    import { notify } from '../../modules/notifications';
    import Button from '../Button.svelte';
    import {
        createMountController,
        defaultMountApi,
        type MountController,
    } from './mount-controller';

    interface Props {
        controller?: MountController;
        mode?: 'toolbar' | 'menu';
        onMenuAction?: () => void;
    }

    let {
        controller = createMountController(defaultMountApi, notify),
        mode = 'toolbar',
        onMenuAction = () => undefined,
    }: Props = $props();

    onMount(() => {
        void controller.refresh();
    });

    const mountedTitle = $derived(
        $controller.location
            ? `${$controller.label} - ${$controller.location}`
            : `${$controller.label} - ${$controller.mode}`,
    );
    const modeLabel = $derived.by(() => {
        if ($controller.mode === 'read-only') return 'Read only';
        if ($controller.writeState === 'ready' && $controller.acceptingWrites) return 'Read/write';
        if ($controller.writeState === 'draining' || $controller.writeState === 'drained') return 'Writes paused';
        return 'Preparing writes';
    });
    const ejectLabel = $derived.by(() => {
        if ($controller.phase !== 'disconnecting') return 'Eject Tdrive';
        if ($controller.mode === 'read-write' && $controller.writeState === 'draining') {
            if ($controller.activeWrites === 1) return 'Finishing 1 change...';
            if ($controller.activeWrites > 1) return `Finishing ${$controller.activeWrites} changes...`;
            return 'Finishing changes...';
        }
        return 'Ejecting Tdrive...';
    });
</script>

<div
    class="mount-control"
    class:menu-mode={mode === 'menu'}
    role={mode === 'menu' ? 'group' : undefined}
    aria-label={mode === 'menu' ? `${$controller.label} mount controls` : undefined}
    aria-live="polite"
>
    {#if mode === 'menu'}
        {#if $controller.mounted}
            <button
                id="disconnect-mounted-drive-button"
                class="profile-menu-item mount-menu-item"
                type="button"
                role="menuitem"
                disabled={$controller.phase === 'disconnecting'}
                aria-busy={$controller.phase === 'disconnecting' ? 'true' : undefined}
                aria-label="Eject Tdrive"
                onclick={() => {
                    onMenuAction();
                    void controller.disconnect();
                }}
            >
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path stroke-linecap="round" stroke-linejoin="round" d="M8 12h8m-4-4l4 4-4 4"/><path stroke-linecap="round" stroke-linejoin="round" d="M5 3h14a2 2 0 012 2v14a2 2 0 01-2 2H5a2 2 0 01-2-2V5a2 2 0 012-2z"/></svg>
                <span class="mount-menu-copy">
                    <span>{ejectLabel}</span>
                    {#if $controller.mode === 'read-write' && $controller.phase !== 'disconnecting'}
                        <span class="mount-mode-label">{modeLabel}</span>
                    {/if}
                </span>
                {#if $controller.phase === 'disconnecting'}
                    <span class="mount-menu-spinner" aria-hidden="true"></span>
                {/if}
            </button>
        {:else}
            <button
                id="mount-drive-button"
                class="profile-menu-item mount-menu-item"
                type="button"
                role="menuitem"
                disabled={$controller.phase === 'mounting'}
                aria-busy={$controller.phase === 'mounting' ? 'true' : undefined}
                aria-label={`Mount ${$controller.label}`}
                onclick={() => {
                    onMenuAction();
                    void controller.mount();
                }}
            >
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path stroke-linecap="round" stroke-linejoin="round" d="M4 6a2 2 0 012-2h12a2 2 0 012 2v12a2 2 0 01-2 2H6a2 2 0 01-2-2V6z"/><path stroke-linecap="round" stroke-linejoin="round" d="M8 15h8m-4-4v8"/></svg>
                <span class="mount-menu-title">
                    {$controller.phase === 'mounting' ? `Mounting ${$controller.label}...` : 'Mount'}
                </span>
                {#if $controller.phase === 'mounting'}
                    <span class="mount-menu-spinner" aria-hidden="true"></span>
                {/if}
            </button>
        {/if}
    {:else if $controller.mounted}
        <div class="mounted-actions" role="group" aria-label={`${$controller.label} mount controls`}>
            <span class="mounted-label" title={mountedTitle}>
                <span class="mounted-dot" aria-hidden="true"></span>
                <span>{$controller.label}</span>
                <span class="mount-mode-label">{modeLabel}</span>
            </span>
            <Button
                id="disconnect-mounted-drive-button"
                size="sm"
                variant="secondary"
                loading={$controller.phase === 'disconnecting'}
                aria-label="Eject Tdrive"
                onclick={() => void controller.disconnect()}
            >
                {ejectLabel}
            </Button>
        </div>
    {:else}
        <Button
            id="mount-drive-button"
            size="sm"
            variant="secondary"
            loading={$controller.phase === 'mounting'}
            aria-label={`Mount ${$controller.label}`}
            onclick={() => void controller.mount()}
        >
            {$controller.phase === 'mounting' ? 'Mounting...' : 'Mount'}
        </Button>
    {/if}

    {#if $controller.phase === 'error'}
        <span class="mount-announcement" role="alert">
            {$controller.error || 'The drive operation failed. Try again.'}
        </span>
    {/if}
</div>

<style>
    .mount-control,
    .mounted-actions,
    .mounted-label {
        display: inline-flex;
        align-items: center;
    }

    .mount-control {
        position: relative;
        flex: 0 0 auto;
    }

    .mount-control.menu-mode {
        display: block;
        width: 100%;
    }

    .mount-menu-item {
        min-width: 0;
    }

    .mount-menu-item:disabled {
        cursor: wait;
        opacity: 0.65;
    }

    .mount-menu-title {
        min-width: 0;
    }

    .mount-menu-copy {
        display: flex;
        min-width: 0;
        flex: 1;
        flex-direction: column;
        align-items: flex-start;
    }

    .mount-menu-title {
        flex: 1;
    }

    .mount-menu-title {
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
    }

    .mount-menu-spinner {
        width: 13px;
        height: 13px;
        flex: 0 0 auto;
        margin-left: auto;
        border: 2px solid currentColor;
        border-top-color: transparent;
        border-radius: var(--radius-pill);
        animation: mount-menu-spin 720ms linear infinite;
    }

    .mounted-actions {
        gap: var(--space-2);
    }

    .mounted-label {
        min-height: 32px;
        max-width: 190px;
        gap: 6px;
        padding: 0 10px;
        border: 1px solid color-mix(in srgb, var(--success) 34%, var(--border));
        border-radius: var(--radius-md);
        color: var(--text-main);
        background: color-mix(in srgb, var(--success) 8%, transparent);
        font-size: var(--font-size-xs);
        font-weight: 800;
        white-space: nowrap;
    }

    .mounted-dot {
        width: 7px;
        height: 7px;
        flex: 0 0 auto;
        border-radius: var(--radius-pill);
        background: var(--success);
        box-shadow: 0 0 0 3px color-mix(in srgb, var(--success) 16%, transparent);
    }

    .mount-mode-label {
        color: var(--text-muted);
        font-size: 0.66rem;
        font-weight: 700;
        letter-spacing: 0.02em;
        text-transform: uppercase;
    }

    .mount-announcement {
        position: absolute;
        width: 1px;
        height: 1px;
        padding: 0;
        margin: -1px;
        overflow: hidden;
        clip: rect(0, 0, 0, 0);
        white-space: nowrap;
        border: 0;
    }

    @media (max-width: 1120px) {
        .mount-mode-label {
            display: none;
        }

        .mounted-label {
            max-width: 132px;
        }
    }

    @keyframes mount-menu-spin {
        to { transform: rotate(360deg); }
    }

    @media (prefers-reduced-motion: reduce) {
        .mount-menu-spinner {
            animation: none;
        }
    }
</style>
