<script lang="ts">
    import { tick } from 'svelte';
    import ChevronLeftIcon from '@lucide/svelte/icons/chevron-left';
    import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
    import FolderIcon from '@lucide/svelte/icons/folder';
    import FolderPlusIcon from '@lucide/svelte/icons/folder-plus';
    import { isMobilePlatform } from '../../api';
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
    import ModalShell from './ModalShell.svelte';
    import { pushSheet } from './sheet-stack';
    import { openActivity } from '../notifications/activity-store';

    let makingFolder = $state(false);
    let folderName = $state('');
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
    initialFocus="#source-copy-dismiss"
    restoreFocus={sharedCopy ? '#file-list' : '.channel-row-main'}
    onClose={closeSourceCopy}
>
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
            {#if makingFolder}
                <div class="copy-folder-editor">
                    <label class="field-label" for="source-copy-folder-name">Folder name</label>
                    <input id="source-copy-folder-name" type="text" autocomplete="off" bind:value={folderName} disabled={!editing} onkeydown={(event) => {
                        if (event.key === 'Enter' && editing && folderName.trim()) void makeFolder();
                    }} />
                    <div class="copy-folder-buttons">
                        <button type="button" class="secondary-btn" disabled={!editing} onclick={() => { makingFolder = false; }}>Cancel</button>
                        <button type="button" class="secondary-btn" disabled={!editing || !folderName.trim()} onclick={() => void makeFolder()}>Create folder</button>
                    </div>
                </div>
            {:else}
                <button type="button" class="secondary-btn copy-new-folder" disabled={!editing} onclick={() => {
                    makingFolder = true;
                    folderName = $dialog?.suggestedFolder ?? '';
                }}><FolderPlusIcon size={16} aria-hidden="true" /> New folder</button>
            {/if}
            {#if $dialog.items.length === 1}
                <label class="field-label" for="source-copy-file-name">File name</label>
                <input id="source-copy-file-name" type="text" value={$dialog.items[0].name} disabled={!editing} oninput={(event) => editSourceCopyName(event.currentTarget.value)} autocomplete="off" />
            {:else}
                <details class="copy-files">
                    <summary>{$dialog.items.length} files selected</summary>
                    <ul>
                        {#each $dialog.items as item (item.source.msg_id)}
                            <li>{item.name}{#if item.originalName && item.originalName !== item.name} <span class="copy-muted">· was {item.originalName}</span>{/if}</li>
                        {/each}
                    </ul>
                </details>
            {/if}
            <p class="copy-muted">Copies are saved without vault encryption.{sharedCopy ? '' : ' The original files stay in Telegram.'}</p>
            {#if stage === 'saving'}<p role="status">{$dialog.stopping ? 'Stopping after the current work settles…' : sharedCopy ? 'Copying files…' : 'Saving files…'}</p>{/if}
        {/if}
        {#if $dialog.error}<p class="modal-error" role="alert">{$dialog.error}</p>{/if}
    {/if}

    {#snippet actions()}
        {#if stage === 'saving'}
            <button class="secondary-btn" type="button" disabled={$dialog?.stopping} onclick={() => void stopSourceCopy()}>{sharedCopy ? 'Stop copying' : 'Stop saving'}</button>
        {:else}
            <button id="source-copy-dismiss" class="secondary-btn" type="button" disabled={stage === 'creating'} onclick={closeSourceCopy}>{stage === 'done' ? 'Done' : 'Cancel'}</button>
            {#if stage === 'done'}
                {#if anySaved}<button class="primary-btn" type="button" onclick={() => void openSavedSourceCopyFolder()}>Open folder</button>{/if}
                {#if !$dialog?.results.length || $dialog.results.some(({ status }) => status === 'pending')}
                    <button class="secondary-btn" type="button" onclick={() => void viewActivity()}>View Activity</button>
                {/if}
            {:else}
                <button class="primary-btn" type="button" disabled={!editing || makingFolder || $dialog?.items.some(({ name }) => !name.trim())} onclick={() => void confirmSourceCopy()}>{sharedCopy ? 'Copy here' : 'Save here'}</button>
            {/if}
        {/if}
    {/snippet}
</ModalShell>

<style>
    :global(.source-copy-card) { max-width: 490px; }
    .move-list { max-height: min(220px, 30dvh); min-height: 54px; }
    .copy-new-folder { display: flex; align-items: center; gap: var(--space-2); margin-block: var(--space-3); }
    .copy-folder-editor { margin-block: var(--space-3); }
    .copy-folder-buttons { display: flex; justify-content: flex-end; gap: var(--space-2); margin-block-start: var(--space-2); }
    .copy-muted { color: var(--color-text-muted); font-size: var(--type-xs); line-height: var(--leading-normal); }
    .copy-files { margin-block: var(--space-3); }
    .copy-files ul, .copy-results { max-height: 220px; overflow: auto; overflow-wrap: anywhere; }
    .copy-results { padding: 0; list-style: none; }
    .copy-results li { display: grid; gap: var(--space-1); padding-block: var(--space-2); }
    .copy-results li + li { border-top: 1px solid var(--color-border-soft); }
    .copy-results li span { color: var(--color-text-muted); font-size: var(--type-xs); }
</style>
