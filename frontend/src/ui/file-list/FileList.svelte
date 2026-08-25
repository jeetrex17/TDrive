<script lang="ts">
    import DownloadIcon from '@lucide/svelte/icons/download';
    import ExternalLinkIcon from '@lucide/svelte/icons/external-link';
    import FolderIcon from '@lucide/svelte/icons/folder';
    import LockKeyholeIcon from '@lucide/svelte/icons/lock-keyhole';
    import PlayIcon from '@lucide/svelte/icons/play';
    import FileState from './FileState.svelte';
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

    // Keyboard handling is delegated once on #file-list. Keeping this no-op on
    // the row makes Svelte's a11y contract explicit without adding per-row
    // behavior or fighting the existing roving-focus controller.
    function onDelegatedKeydown() {}

    const visibleRows = $derived($fileListView.kind === 'rows'
        ? sortFileListRows($fileListView.rows, $fileSortState)
        : []);
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
    {#each visibleRows as row (row.key)}
        {#if row.kind === 'pending-folder'}
            <div
                class="file-row drive-row folder-row pending-folder"
                data-type="pending-folder"
                data-temp-id={row.tempId}
                title="Creating..."
            >
                <div class="row-name" title={row.name}>
                    <span class="folder-chip" aria-hidden="true">
                        <FolderIcon size={18} strokeWidth={2} aria-hidden="true" />
                    </span>
                    <span class="row-label">{row.name}</span>
                    <span class="pending-indicator" aria-hidden="true"></span>
                </div>
                <div class="row-meta">Creating...</div>
                <div class="row-meta">—</div>
                <div class="row-actions"></div>
            </div>
        {:else}
            <!-- Events stay delegated on #file-list, but selected/focused row
                 visuals are model-driven here so keyed Svelte updates cannot
                 clobber accessibility state. -->
            <div
                class={`file-row drive-row${row.kind === 'folder' ? ' folder-row' : ''}${$selectedFileRowKeys.has(row.selectionKey) ? ' is-selected' : ''}${$activeFileRowKey === row.selectionKey ? ' is-keyboard-active' : ''}`}
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
                role="option"
                aria-selected={$selectedFileRowKeys.has(row.selectionKey) ? 'true' : 'false'}
                aria-label={row.ariaLabel}
                tabindex={$activeFileRowKey === row.selectionKey ? 0 : -1}
                onclick={(event) => onRowClick(event, row)}
                ondblclick={(event) => onRowDoubleClick(event, row)}
                onkeydown={onDelegatedKeydown}
            >
                <div class="row-name" draggable="true" title={row.name}>
                    {#if row.kind === 'folder'}
                        <span class="folder-chip" aria-hidden="true">
                            <FolderIcon size={18} strokeWidth={2} aria-hidden="true" />
                        </span>
                        <span class="row-label">{row.name}</span>
                    {:else}
                        <span class="file-ext-text" aria-hidden="true">{row.ext}</span>
                        {#if row.encrypted}
                            <span class="file-lock-badge" title="Encrypted" aria-label="Encrypted">
                                <LockKeyholeIcon size={12} strokeWidth={2} aria-hidden="true" />
                            </span>
                        {/if}
                        <span class="row-label">{row.baseName}</span>
                        {#if row.uploaderChip}
                            <span class="uploader-chip">{row.uploaderChip.label}</span>
                        {/if}
                    {/if}
                </div>
                <div class="row-meta">{row.metaLabel}</div>
                <div class={`row-meta ${row.kind === 'folder' ? 'folder-size' : ''}`}>{row.sizeLabel}</div>
                <div class="row-actions">
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
{/if}
