<script lang="ts">
    import EjectIcon from '@lucide/svelte/icons/eject';
    import HardDriveIcon from '@lucide/svelte/icons/hard-drive';
    import { onMount } from 'svelte';
    import { notify } from '../../modules/notifications';
    import type { MountableDrive } from '../../types';
    import {
        createMountController,
        defaultMountApi,
        type MountController,
    } from './mount-controller';

    interface Props {
        controller?: MountController;
        onMenuAction?: () => void;
        loadDrives?: () => Promise<readonly MountableDrive[]>;
        variant?: 'menu' | 'sidebar';
    }

    let {
        controller = createMountController(defaultMountApi, notify),
        onMenuAction = () => undefined,
        loadDrives,
        variant = 'menu',
    }: Props = $props();

    let loadingDrives = $derived(controller.loadingDrives);

    onMount(() => {
        void controller.refresh();
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
    role="group"
    aria-label={`${$controller.label} mount controls`}
    aria-live="polite"
>
    {#if $controller.mounted}
        <button
            id="disconnect-mounted-drive-button"
            class={variant === 'menu' ? 'profile-menu-item mount-menu-item' : 'drive-action-btn mount-sidebar-action'}
            type="button"
            role={variant === 'menu' ? 'menuitem' : undefined}
            disabled={$controller.phase === 'disconnecting'}
            aria-busy={$controller.phase === 'disconnecting' ? 'true' : undefined}
            aria-label="Eject Tdrive"
            onclick={() => {
                onMenuAction();
                void controller.disconnect();
            }}
        >
            <EjectIcon class="mount-action-icon" size={16} strokeWidth={2} aria-hidden="true" />
            <span class="mount-menu-title">{ejectLabel}</span>
            {#if $controller.phase === 'disconnecting'}
                <span class="mount-menu-spinner" aria-hidden="true"></span>
            {/if}
        </button>
    {:else}
        <button
            id="mount-drive-button"
            class={variant === 'menu' ? 'profile-menu-item mount-menu-item' : 'drive-action-btn mount-sidebar-action'}
            type="button"
            role={variant === 'menu' ? 'menuitem' : undefined}
            disabled={$loadingDrives || $controller.phase === 'mounting'}
            aria-busy={$loadingDrives || $controller.phase === 'mounting' ? 'true' : undefined}
            aria-label={`Mount ${$controller.label}`}
            onclick={() => {
                void controller.requestMount({ loadDrives, onAction: onMenuAction });
            }}
        >
            <HardDriveIcon class="mount-action-icon" size={16} strokeWidth={2} aria-hidden="true" />
            <span class="mount-menu-title">
                {$loadingDrives ? 'Loading drives...' : $controller.phase === 'mounting' ? `Mounting ${$controller.label}...` : 'Mount'}
            </span>
            {#if $loadingDrives || $controller.phase === 'mounting'}
                <span class="mount-menu-spinner" aria-hidden="true"></span>
            {/if}
        </button>
    {/if}

    {#if $controller.phase === 'error'}
        <span class="mount-announcement" role="alert">
            {$controller.error || 'The drive operation failed. Try again.'}
        </span>
    {/if}
</div>

<style>
    .mount-control {
        position: relative;
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

    .mount-sidebar-action {
        width: 100%;
        min-width: 0;
        justify-content: flex-start;
        text-align: left;
    }

    .mount-sidebar-action:disabled {
        cursor: wait;
        opacity: 0.65;
    }

    .mount-control :global(.mount-action-icon) {
        width: 16px;
        height: 16px;
        flex: 0 0 auto;
    }

    .mount-menu-title {
        min-width: 0;
        flex: 1;
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

    @keyframes mount-menu-spin {
        to { transform: rotate(360deg); }
    }

    @media (prefers-reduced-motion: reduce) {
        .mount-menu-spinner {
            animation: none;
        }
    }
</style>
