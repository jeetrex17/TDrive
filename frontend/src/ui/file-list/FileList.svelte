<script lang="ts">
    import { onMount } from 'svelte';
    import DownloadIcon from '@lucide/svelte/icons/download';
    import ExternalLinkIcon from '@lucide/svelte/icons/external-link';
    import FolderIcon from '@lucide/svelte/icons/folder';
    import LockKeyholeIcon from '@lucide/svelte/icons/lock-keyhole';
    import PlayIcon from '@lucide/svelte/icons/play';
    import FileState from './FileState.svelte';
    import { fileTypeFamily, fileTypeIcon } from './file-type';
    import { sortFileListRows } from './file-sort';
    import { fileListView } from './file-list-store';
    import { fileSortState } from './file-sort-store';
    import { activeFileRowKey, selectedFileRowKeys } from './row-state-store';
    import type { FileListAction, FileListFileRow, FileListRow, FolderListRow } from './types';

    type InteractiveRow = FolderListRow | FileListFileRow;

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

    const ESTIMATED_ROW_HEIGHT = 54;
    const WINDOW_OVERSCAN = 8;
    let scrollTop = $state(0);
    let viewportHeight = $state(0);
    let rowHeight = $state(ESTIMATED_ROW_HEIGHT);
    let list: HTMLElement | null = null;

    const visibleRows = $derived($fileListView.kind === 'rows'
        ? sortFileListRows($fileListView.rows, $fileSortState)
        : []);
    const rowWindow = $derived.by(() => {
        if (visibleRows.length <= 64) {
            return { before: 0, rows: visibleRows, start: 0, after: 0 };
        }
        const firstVisible = Math.floor(scrollTop / rowHeight);
        const visibleCount = Math.max(1, Math.ceil(viewportHeight / rowHeight));
        const start = Math.max(0, firstVisible - WINDOW_OVERSCAN);
        const end = Math.min(visibleRows.length, firstVisible + visibleCount + WINDOW_OVERSCAN);
        return {
            before: start * rowHeight,
            rows: visibleRows.slice(start, end),
            start,
            after: (visibleRows.length - end) * rowHeight,
        };
    });

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
            if (footprint > 0) rowHeight = footprint;
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
        const top = index * rowHeight;
        const bottom = top + rowHeight;
        if (top < list.scrollTop || bottom > list.scrollTop + list.clientHeight) {
            list.scrollTop = Math.max(0, top - Math.max(0, (list.clientHeight - rowHeight) / 2));
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

{#if $fileListView.kind === 'state'}
    <FileState
        kind={$fileListView.stateKind}
        title={$fileListView.title}
        body={$fileListView.body ?? ''}
        actionLabel={$fileListView.actionLabel ?? ''}
        onAction={$fileListView.onAction}
    />
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
                    <span class="pending-indicator" aria-hidden="true"></span>
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
