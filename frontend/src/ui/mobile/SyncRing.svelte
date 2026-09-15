<script lang="ts">
    import CheckIcon from '@lucide/svelte/icons/check';
    import type { DriveSyncState } from './mobile-shell-store';

    interface Props {
        status: DriveSyncState;
    }

    let { status }: Props = $props();

    // One 16px ring carries every drive state (spec 2.7): a static track when
    // idle, a spinning arc while syncing, a brief check on success, and a danger
    // track on failure. The arc is a bordered circle with an open top (no raw SVG;
    // the check is a Lucide glyph); only transform/opacity animate.
    const label = $derived(
        status === 'syncing' ? 'Syncing'
        : status === 'synced' ? 'Synced'
        : status === 'failed' ? 'Sync failed'
        : 'In sync',
    );
</script>

<span class="sync-ring" data-state={status} role="img" aria-label={label}>
    {#if status === 'synced'}
        <CheckIcon size={12} strokeWidth={2.75} aria-hidden="true" />
    {:else if status === 'syncing'}
        <span class="sync-spin" aria-hidden="true"></span>
    {:else}
        <span class="sync-track" aria-hidden="true"></span>
    {/if}
</span>

<style>
    .sync-ring {
        display: inline-grid;
        place-items: center;
        width: 16px;
        height: 16px;
        color: var(--color-text-muted);
    }

    .sync-track,
    .sync-spin {
        width: 12px;
        height: 12px;
        border-radius: 50%;
        border: 2px solid currentColor;
    }

    .sync-track { opacity: 0.4; }

    .sync-ring[data-state='syncing'] { color: var(--color-accent); }
    .sync-ring[data-state='synced'] { color: var(--color-success); }
    .sync-ring[data-state='failed'] { color: var(--color-danger); }
    .sync-ring[data-state='failed'] .sync-track { opacity: 0.85; }

    /* Open the top of the ring into an arc, then spin it. */
    .sync-spin {
        border-top-color: transparent;
        transform-origin: center;
        animation: sync-ring-spin 1.2s linear infinite;
    }

    @keyframes sync-ring-spin {
        to { transform: rotate(360deg); }
    }

    @media (prefers-reduced-motion: reduce) {
        /* Loops stop under reduced motion; the arc stays as a static marker. */
        .sync-spin { animation: none; }
    }
</style>
