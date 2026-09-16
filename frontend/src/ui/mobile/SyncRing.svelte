<script lang="ts">
    import CheckIcon from '@lucide/svelte/icons/check';
    import LoaderCircleIcon from '@lucide/svelte/icons/loader-circle';
    import CircleIcon from '@lucide/svelte/icons/circle';
    import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
    import type { DriveSyncState } from './mobile-shell-store';

    interface Props {
        status: DriveSyncState;
    }

    let { status }: Props = $props();

    // One 16px mark carries every drive state (spec 2.7): a quiet circle when
    // idle, a spinning loader while syncing, a check on success, and an alert on
    // failure. Every state is a Lucide glyph at the same size and weight, so the
    // mark keeps one shape language as it changes; only the spin animates, and
    // reduced motion stops it.
    const label = $derived(
        status === 'syncing' ? 'Syncing'
        : status === 'synced' ? 'Synced'
        : status === 'failed' ? 'Sync failed'
        : 'In sync',
    );

    const Glyph = $derived(
        status === 'syncing' ? LoaderCircleIcon
        : status === 'synced' ? CheckIcon
        : status === 'failed' ? CircleAlertIcon
        : CircleIcon,
    );
</script>

<span class="sync-ring" data-state={status} role="img" aria-label={label}>
    <Glyph size={14} strokeWidth={2.5} aria-hidden="true" />
</span>

<style>
    .sync-ring {
        display: inline-grid;
        place-items: center;
        width: 16px;
        height: 16px;
        color: var(--color-text-muted);
    }

    .sync-ring[data-state='idle'] { opacity: 0.5; }
    .sync-ring[data-state='syncing'] { color: var(--color-accent); }
    .sync-ring[data-state='synced'] { color: var(--color-success); }
    .sync-ring[data-state='failed'] { color: var(--color-danger); }

    .sync-ring[data-state='syncing'] :global(svg) {
        transform-origin: center;
        animation: sync-ring-spin 1.2s linear infinite;
    }

    @keyframes sync-ring-spin {
        to { transform: rotate(360deg); }
    }

    @media (prefers-reduced-motion: reduce) {
        /* Loops stop under reduced motion; the loader stays as a static mark. */
        .sync-ring[data-state='syncing'] :global(svg) { animation: none; }
    }
</style>
