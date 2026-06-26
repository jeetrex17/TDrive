<script lang="ts">
    import FileState from './FileState.svelte';
    import { fileListView } from './file-list-store';
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
    {#each $fileListView.rows as row (row.key)}
        {#if row.kind === 'pending-folder'}
            <div
                class="file-row drive-row folder-row pending-folder"
                data-type="pending-folder"
                data-temp-id={row.tempId}
                title="Creating..."
            >
                <div class="row-name">
                    <span class="folder-chip" aria-hidden="true">
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                            <path stroke-linecap="round" stroke-linejoin="round" d="M3 7a2 2 0 012-2h5l2 2h7a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2V7z" />
                        </svg>
                    </span>
                    {row.name}
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
                <div class="row-name" draggable="true">
                    {#if row.kind === 'folder'}
                        <span class="folder-chip" aria-hidden="true">
                            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                                <path stroke-linecap="round" stroke-linejoin="round" d="M3 7a2 2 0 012-2h5l2 2h7a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2V7z" />
                            </svg>
                        </span>
                        {row.name}
                    {:else}
                        <span class="file-ext-text" aria-hidden="true">{row.ext}</span>
                        {#if row.encrypted}
                            <span class="file-lock-badge" title="Encrypted" aria-label="Encrypted">
                                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                                    <path stroke-linecap="round" stroke-linejoin="round" d="M5 11h14a1 1 0 011 1v8a1 1 0 01-1 1H5a1 1 0 01-1-1v-8a1 1 0 011-1z" />
                                    <path stroke-linecap="round" stroke-linejoin="round" d="M8 11V7a4 4 0 118 0v4" />
                                </svg>
                            </span>
                        {/if}
                        {row.baseName}
                        <span class="uploader-chip" data-uploader-slot></span>
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
                                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                                    <path stroke-linecap="round" stroke-linejoin="round" d="M9 5H7a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-2" />
                                    <path stroke-linecap="round" stroke-linejoin="round" d="M14 3h7v7" />
                                    <path stroke-linecap="round" stroke-linejoin="round" d="M10 14L21 3" />
                                </svg>
                            {:else if action.kind === 'play'}
                                <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
                                    <path d="M8 5.8c0-.9.98-1.45 1.74-.98l9.08 5.67a1.15 1.15 0 010 1.96l-9.08 5.67A1.15 1.15 0 018 17.14V5.8z" />
                                </svg>
                            {:else}
                                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                                    <path d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4" />
                                </svg>
                            {/if}
                        </button>
                    {/each}
                </div>
            </div>
        {/if}
    {/each}
{/if}
