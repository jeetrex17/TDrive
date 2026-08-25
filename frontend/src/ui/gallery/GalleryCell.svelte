<script lang="ts">
    import ImageOffIcon from '@lucide/svelte/icons/image-off';
    import LockKeyholeIcon from '@lucide/svelte/icons/lock-keyhole';
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
            <LockKeyholeIcon size={13} strokeWidth={2} aria-hidden="true" />
        </span>
    {/if}
    {#if status === 'failed'}
        <span class="gallery-broken">
            <ImageOffIcon size={34} strokeWidth={1.6} aria-hidden="true" />
        </span>
    {/if}
</button>
