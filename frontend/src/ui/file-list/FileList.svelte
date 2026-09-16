<script lang="ts">
    import { SvelteSet } from 'svelte/reactivity';
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
    import { isMobilePlatform } from '../../api';
    import FileState from './FileState.svelte';
    import { minuteTick } from './clock';
    import { fileTypeFamily, fileTypeIcon } from './file-type';
    import { rowMetaLine, splitRowLabel } from './row-meta';
    import { rowOffset, rowWindowFor, type RowMetrics } from './row-window';
    import ItemStatus from '../mobile/ItemStatus.svelte';
    import { itemStateFor, transfersByFile } from '../mobile/item-state-store';
    import { itemStateDescriptor } from '../mobile/item-state';
    import { activeTab } from '../mobile/mobile-shell-store';
    import { sortFileListRows } from './file-sort';
    import { fileListView } from './file-list-store';
    import { fileSortState } from './file-sort-store';
    import { activeFileRowKey, selectedFileRowKeys } from './row-state-store';
    import type { FileListAction, FileListFileRow, FileListRow, FolderListRow } from './types';

    type InteractiveRow = FolderListRow | FileListFileRow;

    // The phone row is a two-line list item rather than a table row; the
    // desktop grid below is untouched.
    const mobile = isMobilePlatform();

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

    const ESTIMATED_ROW_HEIGHT = mobile ? 68 : 54;
    const WINDOW_OVERSCAN = 8;
    let scrollTop = $state(0);
    let viewportHeight = $state(0);
    let rowHeight = $state(ESTIMATED_ROW_HEIGHT);
    // A row that has to explain itself is taller. Measured on its own so one of
    // them cannot be taken for the standard height, which would move every
    // spacer in the list by the difference.
    let explainRowHeight = $state(ESTIMATED_ROW_HEIGHT);
    let list: HTMLElement | null = null;

    const visibleRows = $derived($fileListView.kind === 'rows'
        ? sortFileListRows($fileListView.rows, $fileSortState)
        : []);
    // The files whose row grows that second line. Read off the transfer map,
    // which holds a handful of entries, rather than by resolving a state for
    // every row in the folder.
    const explainedIds = $derived.by(() => {
        const ids = new SvelteSet<string>();
        if (!mobile) return ids;
        for (const fileId of $transfersByFile.keys()) {
            if (itemStateDescriptor(itemStateFor($transfersByFile, fileId)).needsExplanation) ids.add(fileId);
        }
        return ids;
    });
    const rowMetrics = $derived.by((): RowMetrics => ({
        rowHeight,
        tallRowHeight: explainRowHeight,
        tallIndices: explainedIds.size === 0
            ? []
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

    onMount(() => {
        list = document.getElementById('file-list');
        if (!list) return;
        const onScroll = () => updateViewport();
        const resizeObserver = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(updateViewport);
        list.addEventListener('scroll', onScroll, { passive: true });
        resizeObserver?.observe(list);
        window.addEventListener('tdrive:reveal-file-row', revealRow);
        updateViewport();
        return () => {
            list?.removeEventListener('scroll', onScroll);
            resizeObserver?.disconnect();
            window.removeEventListener('tdrive:reveal-file-row', revealRow);
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
                role="row"
                aria-rowindex={rowWindow.start + rowIndex + 2}
                title="Creating..."
            >
                <div class="row-name" role="gridcell" aria-colindex="1" title={row.name}>
                    <span class="folder-chip" aria-hidden="true">
                        <FolderIcon size={20} strokeWidth={1.75} aria-hidden="true" />
                    </span>
                    <span class="row-text">
                        {@render phoneLabel(row.name, false)}
                        <span class="row-sub"><span class="row-sub-text">Creating...</span><span class="pending-indicator" aria-hidden="true"><LoaderCircleIcon size={12} strokeWidth={2.25} aria-hidden="true" /></span></span>
                    </span>
                </div>
                <div class="row-actions" role="gridcell" aria-colindex="4"></div>
            </div>
        {:else}
            {@const selected = $selectedFileRowKeys.has(row.selectionKey)}
            {@const meta = rowMetaLine(row, $minuteTick)}
            {@const state = itemStateFor($transfersByFile, row.id)}
            {@const stateInfo = itemStateDescriptor(state)}
            <div
                class={`file-row drive-row${row.kind === 'folder' ? ' folder-row' : ''}${selected ? ' is-selected' : ''}${$activeFileRowKey === row.selectionKey ? ' is-keyboard-active' : ''}${stateInfo.needsExplanation ? ' needs-explanation' : ''}${cardEdges(rowIndex)}`}
                use:measureRow
                data-type={dataType(row)}
                data-row-key={row.selectionKey}
                data-id={row.id}
                data-name={row.name}
                data-parent-id={row.parentId}
                data-source={row.kind === 'file' ? row.source : undefined}
                data-size={row.kind === 'file' ? String(row.size) : undefined}
                data-uploader-id={row.kind === 'file' ? String(row.uploaderID) : undefined}
                data-upload-time={row.kind === 'file' ? String(row.uploadTime) : undefined}
                data-encrypted={row.kind === 'file' ? String(row.encrypted) : undefined}
                data-can-delete={row.kind === 'file' ? String(row.canDelete) : undefined}
                data-can-rename={row.kind === 'file' ? String(row.canRename) : undefined}
                role="row"
                aria-rowindex={rowWindow.start + rowIndex + 2}
                aria-selected={selected ? 'true' : 'false'}
                aria-label={row.ariaLabel}
                tabindex={$activeFileRowKey === row.selectionKey ? 0 : -1}
                onclick={(event) => onRowClick(event, row)}
                ondblclick={(event) => onRowDoubleClick(event, row)}
                onkeydown={onGridRowKeydown}
            >
                <div class="row-name" role="gridcell" aria-colindex="1" title={row.name}>
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
                        {@const family = fileTypeFamily(row.ext)}
                        {@const TypeIcon = fileTypeIcon(family)}
                        <span class="file-type-icon" data-family={family} aria-hidden="true">
                            <TypeIcon size={20} strokeWidth={1.75} aria-hidden="true" />
                        </span>
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
                    <div class="row-status" role="gridcell" aria-colindex="3">
                        <ItemStatus state={state} onOpenQueue={() => activeTab.set('transfers')} />
                    </div>
                {/if}
                <div class="row-actions" role="gridcell" aria-colindex="4">
                    <button
                        class="action-icon row-more"
                        type="button"
                        aria-label={`More actions for ${row.name}`}
                        aria-haspopup="menu"
                    >
                        <EllipsisIcon size={20} strokeWidth={2} aria-hidden="true" />
                    </button>
                </div>
                {#if mobile}
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
                        <FolderIcon size={17} strokeWidth={1.5} aria-hidden="true" />
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
                class={`file-row drive-row${row.kind === 'folder' ? ' folder-row' : ''}${$selectedFileRowKeys.has(row.selectionKey) ? ' is-selected' : ''}${$activeFileRowKey === row.selectionKey ? ' is-keyboard-active' : ''}`}
                use:measureRow
                data-type={dataType(row)}
                data-row-key={row.selectionKey}
                data-id={row.id}
                data-name={row.name}
                data-parent-id={row.parentId}
                data-source={row.kind === 'file' ? row.source : undefined}
                data-size={row.kind === 'file' ? String(row.size) : undefined}
                data-uploader-id={row.kind === 'file' ? String(row.uploaderID) : undefined}
                data-upload-time={row.kind === 'file' ? String(row.uploadTime) : undefined}
                data-encrypted={row.kind === 'file' ? String(row.encrypted) : undefined}
                data-can-delete={row.kind === 'file' ? String(row.canDelete) : undefined}
                data-can-rename={row.kind === 'file' ? String(row.canRename) : undefined}
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
                            <FolderIcon size={17} strokeWidth={1.5} aria-hidden="true" />
                        </span>
                        <span class="row-label">{row.name}</span>
                    {:else}
                        {@const family = fileTypeFamily(row.ext)}
                        {@const TypeIcon = fileTypeIcon(family)}
                        <span class="file-type-icon" data-family={family} aria-hidden="true">
                            <!-- Lighter than the app default: Lucide's stroke is fixed
                                 against a 24px grid, so it reads heavier the smaller
                                 the glyph is drawn. The folder chip matches. -->
                            <TypeIcon size={17} strokeWidth={1.5} aria-hidden="true" />
                        </span>
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
                            {#if action.kind === 'open'}
                                <ExternalLinkIcon size={16} strokeWidth={2} aria-hidden="true" />
                            {:else if action.kind === 'play'}
                                <PlayIcon size={16} strokeWidth={2} aria-hidden="true" />
                            {:else}
                                <DownloadIcon size={16} strokeWidth={2} aria-hidden="true" />
                            {/if}
                        </button>
                    {/each}
                </div>
            </div>
        {/if}
    {/each}
    <div aria-hidden="true" style:height={`${rowWindow.after}px`}></div>
{/if}
