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
        tabindex?: number;
    }

    let { item, index, tabindex = 0 }: Props = $props();

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

    function register(node: HTMLElement, identity: { msgId: number; revision: number }) {
        registerCell(node, { ...identity, apply });
        return {
            update(nextIdentity: { msgId: number; revision: number }) {
                if (nextIdentity.msgId === identity.msgId && nextIdentity.revision === identity.revision) return;
                identity = nextIdentity;
                status = 'idle';
                src = '';
                detail = '';
                registerCell(node, { ...identity, apply });
            },
            destroy() {
                unregisterCell(node);
            },
        };
    }

    const cellClass = $derived(
        `gallery-cell${status === 'loaded' ? ' is-loaded' : ''}${status === 'loading' ? ' is-loading' : ''}${status === 'failed' || status === 'missing' ? ' is-failed' : ''}${status === 'locked' ? ' is-locked' : ''}${selected ? ' is-selected' : ''}`,
    );
    // The label carries the detail too: a locked or broken cell has to say so
    // out loud, and a phone has no tooltip to fall back on.
    const title = $derived(detail ? `${item.name} — ${detail}` : item.name);
</script>

<button
    type="button"
    class={cellClass}
    data-id={item.msgId}
    data-index={index}
    data-name={item.name}
    {title}
    {tabindex}
    aria-label={title}
    aria-pressed={selecting ? selected : undefined}
    use:register={{ msgId: item.msgId, revision: 'revision' in item ? Number(item.revision) : 0 }}
>
    <img class="gallery-thumb" alt="" width="512" height="512" decoding="async" src={src || undefined} />
    {#if item.encrypted}
        <span class="gallery-lock">
            <LockKeyholeIcon size={13} strokeWidth={2} aria-hidden="true" />
        </span>
    {/if}
    {#if mobile && status === 'locked'}
        <span class="gallery-locked-pill" aria-hidden="true">Locked</span>
    {/if}
    {#if status === 'failed' || status === 'missing'}
        <span class="gallery-broken">
            <ImageOffIcon size={34} strokeWidth={1.6} aria-hidden="true" />
        </span>
    {/if}
    {#if status === 'missing'}<span class="gallery-missing-pill" aria-hidden="true">Preview pending</span>{/if}
    {#if selecting}
        <span class="gallery-check" aria-hidden="true">
            <CheckIcon size={14} strokeWidth={3} aria-hidden="true" />
        </span>
    {/if}
</button>
