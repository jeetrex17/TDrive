<script lang="ts">
    import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
    import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
    import EllipsisIcon from '@lucide/svelte/icons/ellipsis';
    import FoldersIcon from '@lucide/svelte/icons/folders';
    import PlusIcon from '@lucide/svelte/icons/plus';
    import { state as appState } from '../../state';
    import { profileUser } from '../chrome/profile-store';
    import ChannelAvatar from './ChannelAvatar.svelte';
    import { sourceKey, sourcePeerLabel, type ChannelSource } from './channel-model';
    import type { ChannelSourcesState } from './channel-store';

    import {
        loadSourceOrganization,
        openSourceOrganizationManager,
        partitionSourceGroups,
        sourceOrganization,
    } from './source-organization-state';

    interface Props {
        sources: ChannelSourcesState;
        activeKey: string | null;
        loadPhoto: (source: ChannelSource) => Promise<string>;
        onSelect: (source: ChannelSource) => void;
        onActions: (x: number, y: number, source: ChannelSource) => void;
        onAdd: () => void;
        onRetry: () => void;
    }

    let { sources, activeKey, loadPhoto, onSelect, onActions, onAdd, onRetry }: Props = $props();

    const accountId = $derived(sources.sources[0]?.accountId
        ?? (sources.status === 'ready' ? String(appState.myUserID || $profileUser?.userId || '') : ''));
    const sourceGroups = $derived(partitionSourceGroups(
        sources.sources,
        $sourceOrganization.accountId === accountId ? $sourceOrganization : { accountId, status: 'idle', groups: [], assignments: [] },
    ));
    let collapsed = $state<{ accountId: string; ids: readonly string[] }>({ accountId: '', ids: [] });

    $effect(() => { void loadSourceOrganization(accountId); });

    function isExpanded(id: string): boolean {
        return collapsed.accountId !== accountId || !collapsed.ids.includes(id);
    }

    function toggleGroup(id: string): void {
        const ids = collapsed.accountId === accountId
            ? collapsed.ids.filter((key) => key !== id && sourceGroups.groups.some((group) => group.id === key))
            : [];
        collapsed = { accountId, ids: isExpanded(id) ? [...ids, id] : ids };
    }

    function pointBelow(element: HTMLElement): { x: number; y: number } {
        const rect = element.getBoundingClientRect();
        return { x: rect.left, y: rect.bottom + 4 };
    }

    function openActions(event: MouseEvent, source: ChannelSource): void {
        event.preventDefault();
        // A row click would both open a channel and its menu; the "…" is a
        // sibling of the row button, so stop it reaching the row.
        event.stopPropagation();
        const point = event.clientX || event.clientY
            ? { x: event.clientX, y: event.clientY }
            : pointBelow(event.currentTarget as HTMLElement);
        onActions(point.x, point.y, source);
    }

    // Desktop keeps no visible "…" on channel rows (owner's decision), so the
    // keyboard reaches the same actions the way it reaches any context menu:
    // the Menu key, or Shift+F10, on the focused row.
    function onRowKeydown(event: KeyboardEvent, source: ChannelSource): void {
        if (event.key !== 'ContextMenu' && !(event.shiftKey && event.key === 'F10')) return;
        event.preventDefault();
        const point = pointBelow(event.currentTarget as HTMLElement);
        onActions(point.x, point.y, source);
    }
</script>

{#snippet sourceRow(source: ChannelSource)}
    {@const active = sourceKey(source) === activeKey}
    <div class="channel-nav-row" role="group" aria-label={source.title}>
        <button
            class="drive-item channel-nav-item"
            class:active
            type="button"
            data-channel-source-key={sourceKey(source)}
            title={`${source.title} (${sourcePeerLabel(source)})`}
            aria-current={active ? 'page' : undefined}
            onclick={() => onSelect(source)}
            oncontextmenu={(event) => openActions(event, source)}
            onkeydown={(event) => onRowKeydown(event, source)}
        >
            <ChannelAvatar {source} {loadPhoto} />
            <span class="drive-item-title">{source.title}</span>
        </button>
        <!-- Visible only in the phone drive sheet, where a long press has no
             counterpart and iOS fires no contextmenu; the desktop sidebar
             hides it and uses right-click or the keyboard instead. -->
        <button
            class="channel-nav-actions"
            type="button"
            aria-haspopup="menu"
            aria-label={`Actions for ${source.title}`}
            onclick={(event) => openActions(event, source)}
        >
            <EllipsisIcon size={18} strokeWidth={2} aria-hidden="true" />
        </button>
    </div>
{/snippet}

<div class="channel-nav-title">
    <span>Sources</span>
    <div class="channel-nav-tools">
        {#if accountId}
            <button class="channel-nav-add" type="button" title="Manage source groups" aria-label="Manage source groups" onclick={() => openSourceOrganizationManager(accountId)}>
                <FoldersIcon size={14} strokeWidth={2} aria-hidden="true" />
            </button>
        {/if}
        <button class="channel-nav-add" type="button" title="Add a source" aria-label="Add a source" onclick={onAdd}>
            <PlusIcon size={14} strokeWidth={2.25} aria-hidden="true" />
        </button>
    </div>
</div>

<div class="drives-list">
    {#each sourceGroups.groups as group (group.id)}
        {@const expanded = isExpanded(group.id)}
        <section class="channel-nav-group">
            <button class="channel-nav-group-toggle" type="button" data-channel-group-id={group.id} aria-expanded={expanded} onclick={() => toggleGroup(group.id)}>
                <span class="drive-item-title">{group.name}</span>
                {#if expanded}
                    <ChevronDownIcon size={12} strokeWidth={2.25} aria-hidden="true" />
                {:else}
                    <ChevronRightIcon size={12} strokeWidth={2.25} aria-hidden="true" />
                {/if}
            </button>
            {#if expanded}
                <div class="channel-nav-group-members">
                    {#each group.sources as source (sourceKey(source))}
                        {@render sourceRow(source)}
                    {:else}
                        <p class="channel-nav-group-empty">No sources</p>
                    {/each}
                </div>
            {/if}
        </section>
    {/each}
    {#if sourceGroups.groups.length > 0 && sourceGroups.ungrouped.length > 0}
        <div class="channel-nav-ungrouped">Ungrouped</div>
    {/if}
    {#each sourceGroups.ungrouped as source (sourceKey(source))}
        {@render sourceRow(source)}
    {/each}
    {#if sources.sources.length === 0}
        {#if sources.status === 'error'}
            <div class="drive-empty channel-nav-error">
                Sources did not load.
                <button class="link-button" type="button" onclick={onRetry}>Retry</button>
            </div>
        {:else if sources.status === 'ready'}
            <button class="drive-item channel-nav-item" type="button" onclick={onAdd}>
                <span class="channel-nav-add-icon" aria-hidden="true"><PlusIcon size={12} strokeWidth={2.5} /></span>
                <span class="drive-item-title">Add a source</span>
            </button>
        {/if}
    {/if}
</div>

<style>
    .channel-nav-title {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: var(--space-2);
        padding: 0 4px 4px 8px;
        color: var(--color-text-subtle);
        font-size: var(--type-xs);
        font-weight: var(--weight-medium);
    }

    .channel-nav-tools { display: flex; align-items: center; }

    .channel-nav-add {
        display: inline-grid;
        width: 22px;
        height: 22px;
        place-items: center;
        border: 0;
        border-radius: var(--radius-sm);
        color: var(--text-muted);
        background: transparent;
        cursor: pointer;
        transition:
            background-color var(--motion-fast) var(--ease-standard),
            color var(--motion-fast) var(--ease-standard);
    }

    .channel-nav-add:hover {
        color: var(--text-main);
        background: var(--bg-panel);
    }

    .channel-nav-add:focus-visible {
        outline: none;
        box-shadow: var(--focus-ring);
    }

    /* Row and its trailing "…". On desktop the row is the only column and the
       action button is hidden; the phone sheet adds the button's column. */
    .channel-nav-row {
        display: grid;
        grid-template-columns: minmax(0, 1fr);
        align-items: center;
        min-width: 0;
    }
    .channel-nav-row > .drive-item { min-width: 0; }
    .channel-nav-actions { display: none; }

    /* The channel's own picture takes the drive row's icon column. The gap
       gives back the 2px the avatar is wider than an icon, so channel names
       start where drive names do. */
    .channel-nav-item {
        --avatar-size: 20px;
        gap: 10px;
    }

    .channel-nav-add-icon {
        width: 20px;
        height: 20px;
        flex: 0 0 auto;
        display: grid;
        place-items: center;
        border-radius: var(--radius-pill);
        color: var(--text-muted);
        box-shadow: inset 0 0 0 1px var(--border);
    }

    .channel-nav-item:focus-visible {
        outline: none;
        box-shadow: var(--focus-ring);
    }

    .channel-nav-group { min-width: 0; }

    /* A group is a plain heading in the sidebar's own label style that folds
       its sources away; its rows line up with every other row. */
    .channel-nav-group-toggle {
        display: flex;
        align-items: center;
        gap: 4px;
        width: 100%;
        min-width: 0;
        min-height: 28px;
        margin-top: var(--space-1);
        padding: 0 8px;
        border: 0;
        border-radius: var(--radius-sm);
        background: transparent;
        color: var(--color-text-subtle);
        font: inherit;
        font-size: var(--type-xs);
        font-weight: var(--weight-medium);
        text-align: left;
        cursor: pointer;
        transition: color var(--motion-fast) var(--ease-standard);
    }

    .channel-nav-group-toggle .drive-item-title { flex: 0 1 auto; }
    .channel-nav-group-toggle :global(svg) { flex: 0 0 auto; opacity: 0.7; }

    @media (hover: hover) {
        .channel-nav-group-toggle:hover { color: var(--color-text); }
    }

    .channel-nav-group-toggle:focus-visible { outline: none; box-shadow: var(--focus-ring); }

    .channel-nav-group-members {
        display: flex;
        flex-direction: column;
        gap: 2px;
    }

    .channel-nav-group-empty,
    .channel-nav-ungrouped {
        margin: 0;
        padding: 6px 8px;
        color: var(--color-text-subtle);
        font-size: var(--type-xs);
    }

    .channel-nav-ungrouped { margin-top: var(--space-1); font-weight: var(--weight-medium); }

    .channel-nav-error {
        display: flex;
        align-items: baseline;
        gap: var(--space-2);
        opacity: 1;
    }

    /* In the drive switcher the rows are touch-sized by the sheet, and the
       title row reads as the group's label with the add action beside it. */
    /* The 6px on the right is the add button's enlarged hit area: inside the
       row it lines the glyph up with the drive rows' action buttons, and it
       keeps the switcher from scrolling sideways. */
    :global(html.mobile) .channel-nav-title {
        padding: var(--space-4) 6px var(--space-1) 12px;
        font-size: var(--mobile-type-meta);
    }

    :global(html.mobile) .channel-nav-add {
        position: relative;
        width: 32px;
        height: 32px;
    }

    :global(html.mobile) .channel-nav-add::after {
        position: absolute;
        inset: -6px;
        content: '';
    }

    :global(html.mobile) .channel-nav-tools { gap: var(--space-3); }
    :global(html.mobile) .channel-nav-group-toggle { min-height: var(--touch-target); }

    :global(html.mobile) .channel-nav-item {
        --avatar-size: 28px;
        gap: 12px;
    }

    /* The sheet gives each channel row the visible "…" the shared drives have,
       on a 44px target. */
    :global(html.mobile) .channel-nav-row {
        grid-template-columns: minmax(0, 1fr) var(--touch-target);
        width: 100%;
    }
    :global(html.mobile) .channel-nav-actions {
        display: inline-grid;
        place-items: center;
        width: var(--touch-target);
        height: var(--touch-target);
        border: 0;
        border-radius: var(--radius-sm);
        color: var(--text-muted);
        background: transparent;
        cursor: pointer;
    }
    :global(html.mobile) .channel-nav-actions:active {
        background: var(--color-surface-2);
        color: var(--text-main);
    }
    :global(html.mobile) .channel-nav-actions:focus-visible {
        outline: none;
        box-shadow: var(--focus-ring);
    }

    :global(html.mobile) .channel-nav-add-icon {
        width: 28px;
        height: 28px;
    }

    @media (prefers-reduced-motion: reduce) {
        .channel-nav-add {
            transition: none;
        }
    }
</style>
