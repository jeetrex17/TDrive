<script lang="ts">
    import { tick } from 'svelte';
    import ChevronLeftIcon from '@lucide/svelte/icons/chevron-left';
    import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
    import FilesIcon from '@lucide/svelte/icons/files';
    import FolderIcon from '@lucide/svelte/icons/folder';
    import FolderPlusIcon from '@lucide/svelte/icons/folder-plus';
    import ShieldOffIcon from '@lucide/svelte/icons/shield-off';
    import { isMobilePlatform } from '../../api';
    import { splitNameAndExt } from '../../utils';
    import {
        addSourceCopyFolder,
        browseSourceCopyCrumb,
        closeSourceCopy,
        confirmSourceCopy,
        editSourceCopyName,
        openSavedSourceCopyFolder,
        openSourceCopyFolder,
        reloadSourceCopyFolder,
        sourceCopyDialog as dialog,
        stopSourceCopy,
    } from '../../modules/source-copies';
    import { fileTypeFamily, fileTypeIcon } from '../file-list/file-type';
    import ModalShell from './ModalShell.svelte';
    import { pushSheet } from './sheet-stack';
    import { openActivity } from '../notifications/activity-store';

    let makingFolder = $state(false);
    let folderName = $state('');
    let folderNameInput = $state<HTMLInputElement | null>(null);
    let newFolderButton = $state<HTMLButtonElement | null>(null);
    const stage = $derived($dialog?.stage);
    const editing = $derived(stage === 'ready' && !$dialog?.browseFailed);
    const anySaved = $derived(Boolean($dialog?.results.some(({ status }) => status === 'saved')));
    const sharedCopy = $derived(Boolean($dialog?.fromSharedDrive));
    const names = $derived({
        saved: sharedCopy ? 'Copied' : 'Saved',
        failed: sharedCopy ? 'Could not copy' : 'Could not save',
        pending: 'Check in Activity',
        canceled: 'Canceled',
    });

    $effect(() => {
        if ($dialog) return;
        makingFolder = false;
        folderName = '';
    });

    $effect(() => {
        if (!isMobilePlatform() || !editing || !$dialog?.path.length) return;
        const handle = pushSheet(() => browseSourceCopyCrumb(($dialog?.path.length ?? 0) - 2));
        return () => handle.release();
    });

    async function startFolder(): Promise<void> {
        makingFolder = true;
        folderName = $dialog?.suggestedFolder ?? '';
        await tick();
        folderNameInput?.focus();
    }

    async function cancelFolder(): Promise<void> {
        makingFolder = false;
        folderName = '';
        await tick();
        newFolderButton?.focus();
    }

    async function makeFolder(): Promise<void> {
        const before = $dialog?.path;
        const adjusted = await addSourceCopyFolder(folderName);
        if (adjusted) {
            folderName = adjusted;
        } else if ($dialog?.path !== before) {
            makingFolder = false;
            folderName = '';
        }
    }

    async function viewActivity(): Promise<void> {
        closeSourceCopy();
        await tick();
        openActivity();
    }
</script>

<ModalShell
    hostId="source-copy-modal"
    open={$dialog !== null}
    title={sharedCopy ? 'Copy to My Drive' : 'Save to My Drive'}
    titleId="source-copy-title"
    cardClass="source-copy-card"
    actionsClass="modal-actions source-copy-actions"
    initialFocus="#source-copy-dismiss"
    restoreFocus={sharedCopy ? '#file-list' : '.channel-row-main'}
    dismissible={stage !== 'saving' && stage !== 'creating' && !$dialog?.openingFolder}
    onClose={closeSourceCopy}
>
    <div class="copy-body">
        {#if $dialog}
            {#if stage === 'done'}
                {#if $dialog.results.length}
                    <ul class="copy-results" aria-label={sharedCopy ? 'Copy results' : 'Save results'}>
                        {#each $dialog.results as result, index (index)}
                            <li>
                                <strong>{result.name}</strong>
                                <span>{names[result.status]}{result.message ? ` · ${result.message}` : ''}</span>
                            </li>
                        {/each}
                    </ul>
                {/if}
            {:else}
                <div class="copy-destination">
                    <div class="move-modal-nav">
                        <button type="button" class="move-back-btn" aria-label="Parent folder" disabled={!$dialog.path.length || stage !== 'ready'} onclick={() => browseSourceCopyCrumb(($dialog?.path.length ?? 0) - 2)}>
                            <ChevronLeftIcon size={16} aria-hidden="true" />
                        </button>
                        <div class="move-breadcrumb" aria-label="Destination folder">
                            <button type="button" class="move-crumb" disabled={!$dialog.path.length || stage !== 'ready'} onclick={() => browseSourceCopyCrumb(-1)}>My Drive</button>
                            {#each $dialog.path as folder, index (folder.id)}
                                <span class="move-crumb-sep" aria-hidden="true">/</span>
                                <button type="button" class="move-crumb" disabled={index === $dialog.path.length - 1 || stage !== 'ready'} onclick={() => browseSourceCopyCrumb(index)}>{folder.name}</button>
                            {/each}
                        </div>
                    </div>
                    <div class="move-list" aria-label="Folders in My Drive" aria-busy={stage === 'loading'}>
                        {#if stage === 'loading'}
                            <div class="move-list-empty" role="status">Loading folders…</div>
                        {:else if $dialog.browseFailed}
                            <div class="move-list-empty"><button class="link-button" type="button" onclick={reloadSourceCopyFolder}>Try again</button></div>
                        {:else if !$dialog.folders.length}
                            <div class="move-list-empty">No folders here.</div>
                        {:else}
                            {#each $dialog.folders as folder (folder.id)}
                                <button type="button" class="move-item" disabled={!editing} onclick={() => openSourceCopyFolder(folder)}>
                                    <span class="move-item-icon" aria-hidden="true"><FolderIcon size={16} /></span>
                                    <span class="move-item-name">{folder.name}</span>
                                    <ChevronRightIcon size={16} aria-hidden="true" />
                                </button>
                            {/each}
                        {/if}
                    </div>
                </div>
                {#if makingFolder}
                    <div class="copy-folder-editor">
                        <label class="field-label" for="source-copy-folder-name">Folder name</label>
                        <input id="source-copy-folder-name" type="text" autocomplete="off" bind:this={folderNameInput} bind:value={folderName} disabled={!editing} onkeydown={(event) => {
                            if (event.key === 'Enter' && editing && folderName.trim()) void makeFolder();
                        }} />
                        <div class="copy-folder-buttons">
                            <button type="button" class="secondary-btn" disabled={!editing} onclick={() => void cancelFolder()}>Cancel</button>
                            <button type="button" class="secondary-btn" disabled={!editing || !folderName.trim()} onclick={() => void makeFolder()}>Create folder</button>
                        </div>
                    </div>
                {:else}
                    <button type="button" class="secondary-btn copy-new-folder" bind:this={newFolderButton} disabled={!editing} onclick={() => void startFolder()}>
                        <FolderPlusIcon size={16} aria-hidden="true" /> New folder
                    </button>
                {/if}
                {#if $dialog.items.length === 1}
                    <label class="field-label" for="source-copy-file-name">File name</label>
                    <input id="source-copy-file-name" type="text" value={$dialog.items[0].name} disabled={!editing} oninput={(event) => editSourceCopyName(event.currentTarget.value)} autocomplete="off" />
                {:else}
                    <details class="copy-files">
                        <summary>
                            <FilesIcon size={16} aria-hidden="true" />
                            <span>{$dialog.items.length} files selected</span>
                            <ChevronRightIcon class="copy-disclosure-chevron" size={16} aria-hidden="true" />
                        </summary>
                        <!-- svelte-ignore a11y_no_noninteractive_tabindex (The capped list needs keyboard scrolling.) -->
                        <ul aria-label="Selected files" role="list" tabindex="0">
                            {#each $dialog.items as item (item.source.msg_id)}
                                {@const { base, ext } = splitNameAndExt(item.name)}
                                {@const TypeIcon = fileTypeIcon(fileTypeFamily(ext))}
                                <li>
                                    <span class="copy-file-icon" aria-hidden="true"><TypeIcon size={16} strokeWidth={1.75} /></span>
                                    <div class="copy-file-text">
                                        <div class="copy-file-name" title={item.name}>
                                            <span class="copy-file-stem">{base}</span><span class="copy-file-ext">{item.name.slice(base.length)}</span>
                                        </div>
                                        {#if item.originalName && item.originalName !== item.name}
                                            <p class="copy-file-original copy-muted" title={item.originalName}>Renamed from {item.originalName}</p>
                                        {/if}
                                    </div>
                                </li>
                            {/each}
                        </ul>
                    </details>
                {/if}
                {#if editing}
                    <p id="source-copy-name-hint" class="copy-muted copy-name-hint">TDrive adds a number to names that are already in use in this folder.</p>
                {/if}
                {#if stage === 'saving'}<p role="status">{$dialog.stopping ? 'Stopping after the current work settles…' : sharedCopy ? 'Copying files…' : 'Saving files…'}</p>{/if}
            {/if}
            {#if $dialog.error}<p class="modal-error" role="alert">{$dialog.error}</p>{/if}
        {/if}
    </div>

    {#snippet actions()}
        {#if $dialog && stage !== 'done'}
            <div class="copy-notice">
                <ShieldOffIcon size={15} aria-hidden="true" />
                <p class="copy-muted">Copies are saved without vault encryption.{sharedCopy ? '' : ' The original files stay in Telegram.'}</p>
            </div>
        {/if}
        <div class="copy-action-buttons">
            {#if stage === 'saving'}
                <button class="secondary-btn" type="button" disabled={$dialog?.stopping} onclick={() => void stopSourceCopy()}>{sharedCopy ? 'Stop copying' : 'Stop saving'}</button>
            {:else}
                <button id="source-copy-dismiss" class="secondary-btn" type="button" disabled={stage === 'creating' || $dialog?.openingFolder} onclick={closeSourceCopy}>{stage === 'done' ? 'Done' : 'Cancel'}</button>
                {#if stage === 'done'}
                    {#if anySaved}
                        <button class="primary-btn" type="button" disabled={$dialog?.openingFolder} onclick={() => void openSavedSourceCopyFolder()}>
                            {$dialog?.openingFolder ? 'Opening folder…' : 'Open folder'}
                        </button>
                    {/if}
                    {#if !$dialog?.results.length || $dialog.results.some(({ status }) => status === 'pending')}
                        <button class="secondary-btn" type="button" disabled={$dialog?.openingFolder} onclick={() => void viewActivity()}>View Activity</button>
                    {/if}
                {:else}
                    <button class="primary-btn" type="button" disabled={!editing || makingFolder || $dialog?.items.some(({ name }) => !name.trim())} aria-describedby={editing ? 'source-copy-name-hint' : undefined} onclick={() => void confirmSourceCopy()}>{sharedCopy ? 'Copy here' : 'Save here'}</button>
                {/if}
            {/if}
        </div>
    {/snippet}
</ModalShell>

<style>
    :global(.source-copy-card) { max-width: 490px; }
    :global(.source-copy-card:not(.modal-sheet)) {
        display: flex;
        flex-direction: column;
        max-height: calc(100dvh - 48px);
    }
    :global(.source-copy-card:not(.modal-sheet)) .copy-body { min-height: 0; overflow: auto; }
    :global(.source-copy-card) :global(.source-copy-actions) {
        display: grid;
        grid-template-columns: minmax(0, 1fr);
        flex: 0 0 auto;
        align-items: stretch;
        gap: var(--space-2);
        padding-block-start: var(--space-3);
    }
    .copy-action-buttons { display: flex; justify-content: flex-end; gap: var(--space-2); }
    :global(html.mobile) .copy-action-buttons { flex-direction: column-reverse; }
    .copy-destination {
        overflow: hidden;
        border: 1px solid var(--color-border-soft);
        border-radius: var(--radius-lg);
    }
    .move-list { max-height: min(220px, 30dvh); min-height: 54px; }
    .copy-new-folder { display: flex; align-items: center; gap: var(--space-2); margin-block: var(--space-3); }
    .copy-folder-editor { margin-block: var(--space-3); }
    .copy-folder-buttons { display: flex; justify-content: flex-end; gap: var(--space-2); margin-block-start: var(--space-2); }
    .copy-muted { color: var(--color-text-muted); font-size: var(--type-xs); line-height: var(--leading-normal); }
    .copy-name-hint { margin-block: var(--space-2) 0; }
    .copy-files {
        margin-block: var(--space-3);
        border: 1px solid var(--color-border-soft);
        border-radius: var(--radius-lg);
        background: var(--overlay-white-1);
    }
    .copy-files summary {
        display: grid;
        grid-template-columns: auto minmax(0, 1fr) auto;
        align-items: center;
        gap: var(--space-2);
        min-height: var(--control-lg);
        padding: var(--space-2) var(--space-3);
        border-radius: inherit;
        color: var(--color-text);
        font-size: var(--type-sm);
        font-weight: var(--weight-semibold);
        cursor: pointer;
        list-style: none;
    }
    .copy-files summary::-webkit-details-marker { display: none; }
    .copy-files summary :global(svg) { color: var(--color-text-muted); }
    .copy-files[open] :global(.copy-disclosure-chevron) { transform: rotate(90deg); }
    .copy-files summary:focus-visible,
    .copy-files ul:focus-visible { outline: 2px solid var(--color-accent); outline-offset: -2px; }
    @media (hover: hover) {
        .copy-files summary:hover { background: var(--overlay-white-1); }
    }
    :global(html.mobile) .copy-files summary { min-height: var(--touch-target); }
    .copy-files ul {
        max-height: min(11rem, 27dvh);
        margin: 0;
        padding: 0;
        overflow-y: auto;
        border-top: 1px solid var(--color-border-soft);
        border-radius: 0 0 var(--radius-lg) var(--radius-lg);
        list-style: none;
    }
    .copy-files li {
        display: grid;
        grid-template-columns: auto minmax(0, 1fr);
        align-items: center;
        gap: var(--space-2);
        padding: var(--space-2) var(--space-3);
    }
    .copy-files li + li { border-top: 1px solid var(--color-border-soft); }
    .copy-file-icon {
        display: grid;
        place-items: center;
        inline-size: var(--control-sm);
        block-size: var(--control-sm);
        border-radius: var(--radius-sm);
        background: var(--color-surface-2);
        color: var(--color-text-muted);
    }
    .copy-file-name {
        display: flex;
        align-items: baseline;
        color: var(--color-text);
        font-size: var(--type-sm);
        font-weight: var(--weight-medium);
        line-height: var(--leading-normal);
    }
    .copy-file-stem, .copy-file-ext, .copy-file-original {
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
    }
    .copy-file-ext { flex: 0 0 auto; max-inline-size: 9ch; color: var(--color-text-muted); }
    .copy-file-original { margin: 0; }
    .copy-notice {
        display: flex;
        align-items: flex-start;
        gap: var(--space-2);
        margin-block: 0;
        color: var(--color-text-muted);
    }
    .copy-notice :global(svg) { flex: 0 0 auto; margin-block-start: 2px; }
    .copy-notice p { margin: 0; }
    .copy-results { max-height: 220px; overflow: auto; overflow-wrap: anywhere; }
    .copy-results { padding: 0; list-style: none; }
    .copy-results li { display: grid; gap: var(--space-1); padding-block: var(--space-2); }
    .copy-results li + li { border-top: 1px solid var(--color-border-soft); }
    .copy-results li span { color: var(--color-text-muted); font-size: var(--type-xs); }
</style>
