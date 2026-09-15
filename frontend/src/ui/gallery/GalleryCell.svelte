<script lang="ts">
    import CheckIcon from '@lucide/svelte/icons/check';
    import ImageOffIcon from '@lucide/svelte/icons/image-off';
    import LockKeyholeIcon from '@lucide/svelte/icons/lock-keyhole';
    import { isMobilePlatform } from '../../api';
    import type { FileItem } from '../../types';
    import { selectedFileRowKeys } from '../file-list/row-state-store';
    import { registerCell, unregisterCell, type CellPatch, type CellStatus } from './gallery-controller';

    interface Props {
        item: FileItem;
        index: number;
    }

    let { item, index }: Props = $props();

    let status = $state<CellStatus>('idle');
    let src = $state('');
    let detail = $state('');

    // The phone gallery shares the file list's selection store, so a
    // long-pressed cell shows the same check the rows do.
    const mobile = isMobilePlatform();
    const selecting = $derived(mobile && $selectedFileRowKeys.size > 0);
    const selected = $derived(selecting && $selectedFileRowKeys.has(`file:${item.msgId}`));

    // The controller drives loads/eviction and pushes state here (O(1) per
    // cell, matching the old direct DOM writes).
    function apply(patch: CellPatch): void {
        if (patch.status !== undefined) status = patch.status;
        if (patch.src !== undefined) src = patch.src;
        if (patch.title !== undefined) detail = patch.title;
    }

    function register(node: HTMLElement, msgId: number) {
        registerCell(node, { msgId, apply });
        return {
            update(nextMsgId: number) {
                if (nextMsgId === msgId) return;
                msgId = nextMsgId;
                status = 'idle';
                src = '';
                detail = '';
                registerCell(node, { msgId, apply });
            },
            destroy() {
                unregisterCell(node);
            },
        };
    }

    const cellClass = $derived(
        `gallery-cell${status === 'loaded' ? ' is-loaded' : ''}${status === 'loading' ? ' is-loading' : ''}${status === 'failed' ? ' is-failed' : ''}${status === 'locked' ? ' is-locked' : ''}${selected ? ' is-selected' : ''}`,
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
    aria-pressed={selecting ? selected : undefined}
    use:register={item.msgId}
>
    <img class="gallery-thumb" alt={item.name} decoding="async" src={src || undefined} />
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
    {#if selecting}
        <span class="gallery-check" aria-hidden="true">
            <CheckIcon size={14} strokeWidth={3} aria-hidden="true" />
        </span>
    {/if}
</button>
