<script lang="ts">
    import type { FileItem } from '../../types';
    import { registerCell, unregisterCell, type CellPatch, type CellStatus } from './gallery-controller';

    interface Props {
        item: FileItem;
        index: number;
    }

    let { item, index }: Props = $props();

    let status = $state<CellStatus>('idle');
    let src = $state('');
    let detail = $state('');

    // The controller drives loads/eviction and pushes state here (O(1) per
    // cell, matching the old direct DOM writes).
    function apply(patch: CellPatch): void {
        if (patch.status !== undefined) status = patch.status;
        if (patch.src !== undefined) src = patch.src;
        if (patch.title !== undefined) detail = patch.title;
    }

    function register(node: HTMLElement) {
        registerCell(node, { msgId: item.msgId, apply });
        return {
            destroy() {
                unregisterCell(node);
            },
        };
    }

    const cellClass = $derived(
        `gallery-cell${status === 'loaded' ? ' is-loaded' : ''}${status === 'loading' ? ' is-loading' : ''}${status === 'failed' ? ' is-failed' : ''}${status === 'locked' ? ' is-locked' : ''}`,
    );
    const title = $derived(detail ? `${item.name} — ${detail}` : item.name);
</script>

<button
    type="button"
    class={cellClass}
    data-id={item.msgId}
    data-index={index}
    data-name={item.name}
    {title}
    aria-label={item.name}
    use:register
>
    <img class="gallery-thumb" alt="" decoding="async" src={src || undefined} />
    {#if item.encrypted}
        <span class="gallery-lock">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><rect x="5" y="11" width="14" height="10" rx="2"/><path stroke-linecap="round" stroke-linejoin="round" d="M8 11V7a4 4 0 018 0v4"/></svg>
        </span>
    {/if}
    {#if status === 'failed'}
        <span class="gallery-broken">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true"><rect x="3" y="5" width="18" height="14" rx="2"/><path stroke-linecap="round" stroke-linejoin="round" d="M3 16l5-5 4 4 3-3 6 6"/></svg>
        </span>
    {/if}
</button>
