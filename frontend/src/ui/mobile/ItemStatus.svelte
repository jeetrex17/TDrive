<script lang="ts">
    import CloudIcon from '@lucide/svelte/icons/cloud';
    import CloudDownloadIcon from '@lucide/svelte/icons/cloud-download';
    import ClockIcon from '@lucide/svelte/icons/clock';
    import CircleCheckIcon from '@lucide/svelte/icons/circle-check';
    import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';
    import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';
    import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
    import { itemStateDescriptor, opensQueue, type ItemState } from './item-state';

    interface Props {
        state: ItemState;
        /** Opens the transfer queue. Omitted where there is no queue to open. */
        onOpenQueue?: () => void;
        size?: number;
    }

    let { state, onOpenQueue, size = 16 }: Props = $props();

    // One glyph family, resolved from the state table rather than chosen at
    // each call site, so a new state cannot arrive wearing a borrowed icon.
    const GLYPHS = {
        cloud: CloudIcon,
        'cloud-download': CloudDownloadIcon,
        clock: ClockIcon,
        'circle-check': CircleCheckIcon,
        'refresh-cw': RefreshCwIcon,
        'triangle-alert': TriangleAlertIcon,
        'circle-alert': CircleAlertIcon,
    } as const;

    const info = $derived(itemStateDescriptor(state));
    const Glyph = $derived(GLYPHS[info.glyph as keyof typeof GLYPHS] ?? CloudIcon);
    const tappable = $derived(Boolean(onOpenQueue) && opensQueue(state));
</script>

{#if !info.marked}
    <!-- The resting state draws nothing: see ItemStateDescriptor.marked. -->
{:else if tappable}
    <button
        class="item-status is-tappable"
        data-tone={info.tone}
        data-spins={info.spins ? 'true' : 'false'}
        type="button"
        aria-label={`${info.label}. Open transfers.`}
        onclick={(event) => { event.stopPropagation(); onOpenQueue?.(); }}
    >
        <Glyph {size} strokeWidth={2} aria-hidden="true" />
    </button>
{:else}
    <span
        class="item-status"
        data-tone={info.tone}
        data-spins={info.spins ? 'true' : 'false'}
        role="img"
        aria-label={info.label}
    >
        <Glyph {size} strokeWidth={2} aria-hidden="true" />
    </span>
{/if}
