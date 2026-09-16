<script lang="ts">
    import CheckIcon from '@lucide/svelte/icons/check';
    import LoaderCircleIcon from '@lucide/svelte/icons/loader-circle';
    import CircleIcon from '@lucide/svelte/icons/circle';
    import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
    import type { RingState } from './mobile-shell-store';

    interface Props {
        status: RingState;
        /** Opens the transfer queue. Absent where there is nothing to open. */
        onOpenQueue?: () => void;
    }

    let { status, onOpenQueue }: Props = $props();

    // One 16px mark for every kind of background work: a quiet circle at rest,
    // a spinning loader while anything is moving, an alert when something needs
    // a person. Every state is a Lucide glyph at the same size and weight, so
    // the mark keeps one shape language as it changes; only the spin animates,
    // and reduced motion stops it.
    //
    // The label is the state in words, because a 16px glyph is not a sentence:
    // it is what a screen reader reads and what a returning user needs when the
    // shape alone is ambiguous.
    const label = $derived(
        status === 'active' ? 'Working'
        : status === 'attention' ? 'Needs attention'
        : status === 'failed' ? 'Sync failed'
        : 'Up to date',
    );

    const Glyph = $derived(
        status === 'active' ? LoaderCircleIcon
        : status === 'attention' || status === 'failed' ? CircleAlertIcon
        : status === 'idle' ? CircleIcon
        : CheckIcon,
    );

    // Only the states the queue can explain take a tap. A mark that opens an
    // unrelated screen is worse than one that does nothing.
    const tappable = $derived(Boolean(onOpenQueue) && status !== 'idle');
</script>

{#if tappable}
    <button
        class="sync-ring is-tappable"
        data-state={status}
        type="button"
        aria-label={`${label}. Open transfers.`}
        onclick={() => onOpenQueue?.()}
    >
        <Glyph size={14} strokeWidth={2.5} aria-hidden="true" />
    </button>
{:else}
    <span class="sync-ring" data-state={status} role="img" aria-label={label}>
        <Glyph size={14} strokeWidth={2.5} aria-hidden="true" />
    </span>
{/if}

<style>
    .sync-ring {
        display: inline-grid;
        place-items: center;
        width: 16px;
        height: 16px;
        padding: 0;
        border: 0;
        background: none;
        color: var(--color-text-muted);
    }

    .sync-ring[data-state='idle'] { opacity: 0.5; }
    /* Accent marks work in progress, matching the row badge's rule exactly, so
       the two surfaces speak one colour language. */
    .sync-ring[data-state='active'] { color: var(--color-accent); }
    .sync-ring[data-state='attention'],
    .sync-ring[data-state='failed'] { color: var(--color-danger); }

    .sync-ring.is-tappable {
        /* Grows to a real target without moving the mark or the title beside
           it: the margins give back exactly the 28px the box gained. It grows
           away from the leading edge rather than around it, so the target never
           lands on the drive name's last few pixels and steals its tap. */
        width: 44px;
        height: 44px;
        margin: -14px -20px -14px -8px;
        cursor: pointer;
    }

    .sync-ring.is-tappable:focus-visible {
        outline: 2px solid var(--color-accent);
        outline-offset: -12px;
        border-radius: var(--radius-md);
    }

    .sync-ring[data-state='active'] :global(svg) {
        transform-origin: center;
        animation: sync-ring-spin 1.2s linear infinite;
    }

    @keyframes sync-ring-spin {
        to { transform: rotate(360deg); }
    }

    @media (prefers-reduced-motion: reduce) {
        /* Loops stop under reduced motion; the loader stays as a static mark,
           and the colour still says that work is happening. */
        .sync-ring[data-state='active'] :global(svg) { animation: none; }
    }
</style>
