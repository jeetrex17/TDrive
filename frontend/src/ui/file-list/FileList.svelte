<script lang="ts">
    import { onMount } from 'svelte';
    import CheckIcon from '@lucide/svelte/icons/check';
    import DownloadIcon from '@lucide/svelte/icons/download';
    import EllipsisIcon from '@lucide/svelte/icons/ellipsis';
    import FolderInputIcon from '@lucide/svelte/icons/folder-input';
    import ExternalLinkIcon from '@lucide/svelte/icons/external-link';
    import FolderIcon from '@lucide/svelte/icons/folder';
    import LoaderCircleIcon from '@lucide/svelte/icons/loader-circle';
    import LockKeyholeIcon from '@lucide/svelte/icons/lock-keyhole';
    import PlayIcon from '@lucide/svelte/icons/play';
    import RotateCcwIcon from '@lucide/svelte/icons/rotate-ccw';
    import Trash2Icon from '@lucide/svelte/icons/trash-2';
    import { isMobilePlatform } from '../../api';
    import FileState from './FileState.svelte';
    import FileThumbnail from './FileThumbnail.svelte';
    import { minuteTick } from './clock';
    import { fileTypeFamily, fileTypeIcon } from './file-type';
    import { rowMetaLine, splitRowLabel } from './row-meta';
    import { rowOffset, rowWindowFor, type RowMetrics } from './row-window';
    import ItemStatus from '../mobile/ItemStatus.svelte';
    import { explainedFileIds, itemStateFor, transfersByFile } from '../mobile/item-state-store';
    import { busyRowIds } from './busy-rows';
    import { itemStateDescriptor } from '../mobile/item-state';
    import { activeTab } from '../mobile/mobile-shell-store';
    import { fileListView, sortedFileListRows } from './file-list-store';
    import { fileSortState } from './file-sort-store';
    import { activeFileRowKey, selectedFileRowKeys } from './row-state-store';
    import {
        beginFileThumbnailRender,
        rearmFileThumbnailLocked,
        setFileThumbnailRoot,
        teardownFileThumbnails,
    } from './file-thumbnail-controller';
    import type { FileListAction, FileListFileRow, FileListRow, FolderListRow } from './types';

    type InteractiveRow = FolderListRow | FileListFileRow;

    // The phone row is a two-line list item rather than a table row; the
    // desktop grid below is untouched.
    const mobile = isMobilePlatform();

    // A row carries three attributes, and each one has to earn its place: a
    // long folder is thousands of rows, and anything written here is written
    // that many times.
    //
    // `data-row-key` is how a handler holding nothing but an element gets the
    // row back (see row-lookup.ts). `data-type` is what the delegated
    // listeners and the touch binders match on to tell a folder from a file
    // from a folder still being created. `data-name` is there for the
    // end-to-end tests, which assert on sort order and have no other way to
    // read a row's name out of the two different layouts below.
    //
    // Everything else about a row -- its id, size, source, uploader, whether
    // it can be renamed -- lives in the store and is looked up from there.
    // Serialising those as well gave every row a second, untyped copy of
    // itself that was free to disagree with the first.
    function dataType(row: FileListRow) {
        return row.kind === 'pending-folder' ? 'pending-folder' : row.kind;
    }

    function onActionClick(event: MouseEvent, row: FileListRow, action: FileListAction) {
        if (!action.onClick) return;
        event.stopPropagation();
        action.onClick(event, row);
    }

    function onRowClick(event: MouseEvent, row: InteractiveRow) {
        row.onClick?.(event, row);
    }

    function onRowDoubleClick(event: MouseEvent, row: InteractiveRow) {
        row.onDoubleClick?.(event, row);
    }


    // Keyboard commands are owned by the single delegated listener on
    // #file-list. This handler keeps that bubbling contract explicit to Svelte.
    function onGridRowKeydown(_event: KeyboardEvent): void {}

    // Seeds the spacers for the frames before measureRow reports a real row.
    // A wrong seed is visible: the list scrolls to a stale offset and jumps
    // once the measurement lands. Desktop is the 40px chip plus the row's 12px
    // padding either side plus its 2px gap; the phone row is its own
    // min-height, which is taller than the 44px chip it holds.
    const ESTIMATED_ROW_HEIGHT = mobile ? 68 : 66;
    const WINDOW_OVERSCAN = 8;
    let scrollTop = $state(0);
    let viewportHeight = $state(0);
    let rowHeight = $state(ESTIMATED_ROW_HEIGHT);
    // A row that has to explain itself is taller. Measured on its own so one of
    // them cannot be taken for the standard height, which would move every
    // spacer in the list by the difference.
    let explainRowHeight = $state(ESTIMATED_ROW_HEIGHT);
    let list: HTMLElement | null = null;

    const visibleRows = $derived(sortedFileListRows($fileListView, $fileSortState));
    const thumbnailChannelId = $derived(
        visibleRows.find((row): row is FileListFileRow => row.kind === 'file' && row.thumbnail !== undefined)
            ?.thumbnail?.channelId ?? 0,
    );
    $effect(() => beginFileThumbnailRender(thumbnailChannelId));
    // The rows whose second line changes virtual height.
    //
    // The transfer map is rewritten on every progress event, so what this
    // reads decides what a busy upload costs. It names the files, not the
    // rows: the explained ids are bounded by the capped notification history
    // while a folder is not bounded at all, and an unchanged answer is
    // republished by identity so nothing below re-runs (see explainedFileIds).
    //
    // A plain Set, not a SvelteSet: it is built whole and never mutated after,
    // so per-entry reactive sources would be overhead paid once per file for a
    // signal nothing subscribes to.
    const NO_EXPLAINED_IDS: ReadonlySet<string> = new Set<string>();
    const NO_TALL_INDICES: readonly number[] = [];
    const explainedIds = $derived.by(() => {
        // Read for its identity rather than its rows: the view is what changes
        // when the drive does, and a transfer key only names a row in the
        // drive it was scoped to.
        const scope = $fileListView;
        // Desktop draws no explaining line, and returning first is what keeps
        // it from subscribing to the transfer map at all.
        if (!mobile) return NO_EXPLAINED_IDS;
        return explainedFileIds($transfersByFile, scope);
    });
    const rowMetrics = $derived.by((): RowMetrics => ({
        rowHeight,
        tallRowHeight: explainRowHeight,
        // Over the whole folder, because a spacer's height depends on every
        // tall row above it and not only on the ones on screen. It runs when
        // the explained set changes -- a transfer failing -- not when one
        // progresses, which is the difference between once and sixty times a
        // second.
        tallIndices: explainedIds.size === 0
            ? NO_TALL_INDICES
            : visibleRows.reduce<number[]>((indices, row, index) => {
                if (row.kind !== 'pending-folder' && explainedIds.has(row.id)) indices.push(index);
                return indices;
            }, []),
    }));
    const rowWindow = $derived.by(() => {
        if (visibleRows.length <= 64) {
            return { before: 0, rows: visibleRows, start: 0, after: 0, end: visibleRows.length };
        }
        const window = rowWindowFor(visibleRows.length, scrollTop, viewportHeight, WINDOW_OVERSCAN, rowMetrics);
        return { ...window, rows: visibleRows.slice(window.start, window.end) };
    });
    // Selection mode on the phone: once anything is selected every row shows
    // its check so a tap reads as toggling rather than opening.
    const selecting = $derived(mobile && $selectedFileRowKeys.size > 0);

    // Which rows sit at the ends of the card. Marked by the list rather than
    // left to a CSS sibling selector, which can only see the rendered window:
    // between the spacers, a row in the middle of a long folder looks like a
    // first or last child and rounded its corners while scrolling.
    function cardEdges(rowIndex: number): string {
        const index = rowWindow.start + rowIndex;
        return `${index === 0 ? ' is-card-top' : ''}${index === visibleRows.length - 1 ? ' is-card-bottom' : ''}`;
    }

    function updateViewport(): void {
        if (!list) return;
        scrollTop = list.scrollTop;
        viewportHeight = list.clientHeight;
    }

    function measureRow(element: HTMLElement): { destroy: () => void } {
        const update = () => {
            const style = getComputedStyle(element);
            const footprint = Math.ceil(
                element.getBoundingClientRect().height
                + Number.parseFloat(style.marginTop || '0')
                + Number.parseFloat(style.marginBottom || '0'),
            );
            if (footprint <= 0) return;
            if (element.classList.contains('needs-explanation')) explainRowHeight = footprint;
            else rowHeight = footprint;
        };
        update();
        const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(update);
        observer?.observe(element);
        return { destroy: () => observer?.disconnect() };
    }

    function revealRow(event: Event): void {
        const key = (event as CustomEvent<{ key: string }>).detail?.key;
        const index = visibleRows.findIndex((row) => row.kind !== 'pending-folder' && row.selectionKey === key);
        if (!list || index === -1) return;
        const top = rowOffset(index, rowMetrics);
        const bottom = rowOffset(index + 1, rowMetrics);
        if (top < list.scrollTop || bottom > list.scrollTop + list.clientHeight) {
            list.scrollTop = Math.max(0, top - Math.max(0, (list.clientHeight - (bottom - top)) / 2));
        }
        updateViewport();
    }

    function applyMobileListSemantics(): void {
        if (!mobile || !list) return;
        // The shell starts as a desktop grid so it can render before the mobile
        // portal mounts. A phone row is one two-line item, not four cells, and
        // leaving these attributes in place makes a screen reader invent a
        // header and announce a table that is not on screen.
        list.setAttribute('role', 'list');
        list.removeAttribute('aria-colcount');
        list.removeAttribute('aria-rowcount');
        list.removeAttribute('aria-multiselectable');
    }

    onMount(() => {
        list = document.getElementById('file-list');
        if (!list) return;
        setFileThumbnailRoot(list);
        applyMobileListSemantics();
        const unsubscribeListSemantics = mobile
            ? fileListView.subscribe(applyMobileListSemantics)
            : () => {};
        const onScroll = () => updateViewport();
        const resizeObserver = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(updateViewport);
        list.addEventListener('scroll', onScroll, { passive: true });
        resizeObserver?.observe(list);
        window.addEventListener('tdrive:reveal-file-row', revealRow);
        window.addEventListener('tdrive:unlocked', rearmFileThumbnailLocked);
        updateViewport();
        return () => {
            list?.removeEventListener('scroll', onScroll);
            resizeObserver?.disconnect();
            window.removeEventListener('tdrive:reveal-file-row', revealRow);
            window.removeEventListener('tdrive:unlocked', rearmFileThumbnailLocked);
            unsubscribeListSemantics();
            teardownFileThumbnails();
            list = null;
        };
    });
</script>

{#snippet phoneLabel(name: string, split: boolean)}
    {@const label = split ? splitRowLabel(name) : { head: name, tail: '' }}
    <span class="row-label">
        {#if label.tail}
            <span class="row-label-head">{label.head}</span><span class="row-label-tail">{label.tail}</span>
        {:else}
            <span class="row-label-head">{name}</span>
        {/if}
    </span>
{/snippet}

<!-- An action's glyph, in one place: the phone draws the same set at its own
     size, and two copies of this list would be two chances to disagree. -->
{#snippet actionGlyph(action: FileListAction, size: number)}
    {#if action.kind === 'open'}
        <ExternalLinkIcon {size} strokeWidth={2} aria-hidden="true" />
    {:else if action.kind === 'play'}
        <PlayIcon {size} strokeWidth={2} aria-hidden="true" />
    {:else if action.kind === 'restore'}
        <RotateCcwIcon {size} strokeWidth={2} aria-hidden="true" />
    {:else if action.kind === 'purge'}
        <Trash2Icon {size} strokeWidth={2} aria-hidden="true" />
    {:else}
        <DownloadIcon {size} strokeWidth={2} aria-hidden="true" />
    {/if}
{/snippet}

{#if $fileListView.kind === 'state'}
    <FileState
        kind={$fileListView.stateKind}
        title={$fileListView.title}
        body={$fileListView.body ?? ''}
        actionLabel={$fileListView.actionLabel ?? ''}
        onAction={$fileListView.onAction}
        secondaryActionLabel={$fileListView.secondaryActionLabel ?? ''}
        onSecondaryAction={$fileListView.onSecondaryAction}
    />
{:else if mobile}
    <div aria-hidden="true" style:height={`${rowWindow.before}px`}></div>
    {#each rowWindow.rows as row, rowIndex (row.key)}
        {#if row.kind === 'pending-folder'}
            <div
                class={`file-row drive-row folder-row pending-folder${cardEdges(rowIndex)}`}
                use:measureRow
                data-type="pending-folder"
                data-temp-id={row.tempId}
                role="listitem"
                aria-posinset={rowWindow.start + rowIndex + 1}
                aria-setsize={visibleRows.length}
                title="Creating..."
            >
                <div class="row-name" title={row.name}>
                    <span class="folder-chip" aria-hidden="true">
                        <FolderIcon size={20} strokeWidth={1.75} aria-hidden="true" />
                    </span>
                    <span class="row-text">
                        {@render phoneLabel(row.name, false)}
                        <span class="row-sub"><span class="row-sub-text">Creating...</span><span class="pending-indicator" aria-hidden="true"><LoaderCircleIcon size={12} strokeWidth={2.25} aria-hidden="true" /></span></span>
                    </span>
                </div>
                <div class="row-actions"></div>
            </div>
        {:else}
            {@const selected = $selectedFileRowKeys.has(row.selectionKey)}
            {@const meta = rowMetaLine(row, $minuteTick)}
            {@const state = itemStateFor($transfersByFile, row.id)}
            {@const stateInfo = itemStateDescriptor(state)}
            <!-- The focusable item is intentional: the delegated list handler
                 supplies its keyboard contract while the native list role
                 keeps the phone's one-item-per-row announcement. -->
            <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
            <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
            <div
                class={`file-row drive-row${row.kind === 'folder' ? ' folder-row' : ''}${selected ? ' is-selected' : ''}${$activeFileRowKey === row.selectionKey ? ' is-keyboard-active' : ''}${stateInfo.needsExplanation ? ' needs-explanation' : ''}${$busyRowIds.has(row.id) ? ' is-busy' : ''}${cardEdges(rowIndex)}`}
                aria-busy={$busyRowIds.has(row.id) ? 'true' : undefined}
                use:measureRow
                data-type={dataType(row)}
                data-row-key={row.selectionKey}
                data-name={row.name}
                role="listitem"
                aria-posinset={rowWindow.start + rowIndex + 1}
                aria-setsize={visibleRows.length}
                aria-label={selected ? `Selected. ${row.ariaLabel}` : row.ariaLabel}
                tabindex={$activeFileRowKey === row.selectionKey ? 0 : -1}
                onclick={(event) => onRowClick(event, row)}
                ondblclick={(event) => onRowDoubleClick(event, row)}
                onkeydown={onGridRowKeydown}
            >
                <div class="row-name" title={row.name}>
                    {#if selecting}
                        <span class="row-check" aria-hidden="true">
                            <CheckIcon size={14} strokeWidth={3} aria-hidden="true" />
                        </span>
                    {/if}
                    {#if row.kind === 'folder'}
                        <span class="folder-chip" aria-hidden="true">
                            <FolderIcon size={20} strokeWidth={1.75} aria-hidden="true" />
                        </span>
                    {:else}
                        {#if row.thumbnail}
                            <FileThumbnail ext={row.ext} identity={row.thumbnail} />
                        {:else}
                            {@const family = fileTypeFamily(row.ext)}
                            {@const TypeIcon = fileTypeIcon(family)}
                            <span class="file-type-icon" data-family={family} aria-hidden="true">
                                <TypeIcon size={20} strokeWidth={1.75} aria-hidden="true" />
                            </span>
                        {/if}
                    {/if}
                    <span class="row-text">
                        {@render phoneLabel(row.name, row.kind === 'file')}
                        {#if meta || (row.kind === 'file' && (row.encrypted || row.uploaderChip?.firstName))}
                            <span class="row-sub">
                                {#if meta}
                                    <span class="row-sub-text">{meta}</span>
                                {/if}
                                {#if row.kind === 'file' && row.encrypted}
                                    <span class="file-lock-badge" title="Encrypted" aria-label="Encrypted">
                                        <LockKeyholeIcon size={12} strokeWidth={2} aria-hidden="true" />
                                    </span>
                                {/if}
                                {#if row.kind === 'file' && row.uploaderChip?.firstName}
                                    <span class="uploader-chip">
                                        <span class="uploader-initials" aria-hidden="true">{row.uploaderChip.initials}</span>
                                        {row.uploaderChip.firstName}
                                    </span>
                                {/if}
                            </span>
                        {/if}
                        {#if mobile && stateInfo.needsExplanation}
                            <span class="row-explain" data-tone={stateInfo.tone}>{stateInfo.detail}</span>
                        {/if}
                    </span>
                </div>
                {#if mobile}
                    <div class="row-status">
                        <ItemStatus state={state} onOpenQueue={() => activeTab.set('transfers')} />
                    </div>
                {/if}
                <div class="row-actions">
                    {#if row.actionsInline}
                        <!-- Few, important, and with no menu behind them, so the
                             phone shows them the way the desktop does rather
                             than hiding them in a sheet built for a live item. -->
                        {#each row.actions as action (action.kind)}
                            <button
                                class={`action-icon ${action.className}`}
                                type="button"
                                title={action.title}
                                aria-label={action.label}
                                onclick={(event) => onActionClick(event, row, action)}
                            >
                                {@render actionGlyph(action, 20)}
                            </button>
                        {/each}
                    {:else}
                        <button
                            class="action-icon row-more"
                            type="button"
                            aria-label={`More actions for ${row.name}`}
                            aria-haspopup="menu"
                        >
                            <EllipsisIcon size={20} strokeWidth={2} aria-hidden="true" />
                        </button>
                    {/if}
                </div>
                {#if mobile && !row.actionsInline}
                    <!-- Revealed by a trailing swipe. One action, and a
                         reversible one: a destructive button a careless thumb
                         away from a scrolling list is the worst pattern in
                         mobile file managers, and being the platform default
                         does not make it safe. Delete stays in the overflow
                         sheet behind a confirm.

                         Hidden from assistive tech because it duplicates what
                         the always-visible overflow button already offers; a
                         screen reader should not meet the same action twice. -->
                    <div class="row-swipe-actions" aria-hidden="true">
                        <button class="row-swipe-btn" type="button" tabindex="-1" data-swipe-action="move">
                            <FolderInputIcon size={20} strokeWidth={1.9} aria-hidden="true" />
                            <span>Move</span>
                        </button>
                    </div>
                {/if}
            </div>
        {/if}
    {/each}
    <div aria-hidden="true" style:height={`${rowWindow.after}px`}></div>
{:else}
    <div aria-hidden="true" style:height={`${rowWindow.before}px`}></div>
    {#each rowWindow.rows as row, rowIndex (row.key)}
        {#if row.kind === 'pending-folder'}
            <div
                class="file-row drive-row folder-row pending-folder"
                use:measureRow
                data-type="pending-folder"
                data-temp-id={row.tempId}
                role="row"
                aria-rowindex={rowWindow.start + rowIndex + 2}
                title="Creating..."
            >
                <div class="row-name" role="gridcell" aria-colindex="1" title={row.name}>
                    <span class="folder-chip" aria-hidden="true">
                        <FolderIcon size={20} strokeWidth={1.5} aria-hidden="true" />
                    </span>
                    <span class="row-label">{row.name}</span>
                    <span class="pending-indicator" aria-hidden="true"><LoaderCircleIcon size={12} strokeWidth={2.25} aria-hidden="true" /></span>
                </div>
                <div class="row-meta" role="gridcell" aria-colindex="2">Creating...</div>
                <div class="row-meta" role="gridcell" aria-colindex="3"><span aria-hidden="true">…</span></div>
                <div class="row-actions" role="gridcell" aria-colindex="4"></div>
            </div>
        {:else}
            <div
                class={`file-row drive-row${row.kind === 'folder' ? ' folder-row' : ''}${$selectedFileRowKeys.has(row.selectionKey) ? ' is-selected' : ''}${$activeFileRowKey === row.selectionKey ? ' is-keyboard-active' : ''}${$busyRowIds.has(row.id) ? ' is-busy' : ''}`}
                aria-busy={$busyRowIds.has(row.id) ? 'true' : undefined}
                use:measureRow
                data-type={dataType(row)}
                data-row-key={row.selectionKey}
                data-name={row.name}
                role="row"
                aria-rowindex={rowWindow.start + rowIndex + 2}
                aria-selected={$selectedFileRowKeys.has(row.selectionKey) ? 'true' : 'false'}
                aria-label={row.ariaLabel}
                tabindex={$activeFileRowKey === row.selectionKey ? 0 : -1}
                onclick={(event) => onRowClick(event, row)}
                ondblclick={(event) => onRowDoubleClick(event, row)}
                onkeydown={onGridRowKeydown}
            >
                <div class="row-name" role="gridcell" aria-colindex="1" draggable="true" title={row.name}>
                    {#if row.kind === 'folder'}
                        <span class="folder-chip" aria-hidden="true">
                            <FolderIcon size={20} strokeWidth={1.5} aria-hidden="true" />
                        </span>
                        <span class="row-label">{row.name}</span>
                    {:else}
                        {#if row.thumbnail}
                            <FileThumbnail ext={row.ext} identity={row.thumbnail} />
                        {:else}
                            {@const family = fileTypeFamily(row.ext)}
                            {@const TypeIcon = fileTypeIcon(family)}
                            <span class="file-type-icon" data-family={family} aria-hidden="true">
                                <!-- Lighter than the app default: Lucide's stroke is fixed
                                     against a 24px grid, so it reads heavier the smaller
                                     the glyph is drawn. The folder chip matches. -->
                                <TypeIcon size={20} strokeWidth={1.5} aria-hidden="true" />
                            </span>
                        {/if}
                        {#if row.encrypted}
                            <span class="file-lock-badge" title="Encrypted" aria-label="Encrypted">
                                <LockKeyholeIcon size={12} strokeWidth={2} aria-hidden="true" />
                            </span>
                        {/if}
                        <!-- The glyph says document or video; only the name says mkv or mp4. -->
                        <span class="row-label">{row.name}</span>
                        {#if row.uploaderChip}
                            <span class="uploader-chip">{row.uploaderChip.label}</span>
                        {/if}
                    {/if}
                </div>
                <div class="row-meta" role="gridcell" aria-colindex="2">{row.metaLabel}</div>
                <div class={`row-meta ${row.kind === 'folder' ? 'folder-size' : ''}`} role="gridcell" aria-colindex="3">{row.sizeLabel}</div>
                <div class="row-actions" role="gridcell" aria-colindex="4">
                    {#each row.actions as action (action.kind)}
                        <button
                            class={`action-icon ${action.className}`}
                            type="button"
                            title={action.title}
                            aria-label={action.label}
                            onclick={(event) => onActionClick(event, row, action)}
                        >
                            {@render actionGlyph(action, 16)}
                        </button>
                    {/each}
                </div>
            </div>
        {/if}
    {/each}
    <div aria-hidden="true" style:height={`${rowWindow.after}px`}></div>
{/if}
