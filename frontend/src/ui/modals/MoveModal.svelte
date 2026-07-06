<script lang="ts">
    import ModalShell from './ModalShell.svelte';
    import { moveBrowse, moveModal, type MoveFolderEntry } from './move-modal-store';

    interface Props {
        onOpenFolder: (entry: MoveFolderEntry) => void;
        // crumbIndex -1 targets the root ("My Drive"), otherwise a path index.
        onCrumb: (crumbIndex: number) => void;
        onBack: () => void;
        onConfirm: () => void | Promise<void>;
    }

    let { onOpenFolder, onCrumb, onBack, onConfirm }: Props = $props();

    const view = moveModal.state;
    const browse = moveBrowse;

    const currentId = $derived($browse.path[$browse.path.length - 1]?.id ?? '');
    const currentName = $derived($browse.path[$browse.path.length - 1]?.name ?? 'My Drive');
    const confirmDisabled = $derived(
        $view.busy || $browse.blocked.has(currentId) || currentId === $browse.sourceParent,
    );

    function close(): void {
        if ($view.busy) return;
        moveModal.close();
    }

    function confirm(): void {
        if (confirmDisabled) return;
        void onConfirm();
    }
</script>

<ModalShell
    hostId="move-modal"
    open={$view.open}
    titleId="move-modal-title"
    cardClass="move-modal-card"
    actionsClass="move-modal-footer"
    initialFocus="#move-cancel"
    restoreFocus="#file-list"
    onClose={close}
>
    {#snippet header()}
        <div class="move-modal-header">
            <h3 id="move-modal-title" class="modal-title">{$view.payload?.title ?? 'Move item'}</h3>
            <p id="move-modal-subtitle" class="modal-subtitle">Select destination folder</p>
        </div>
    {/snippet}

    <div class="move-modal-nav">
        <button
            id="move-back"
            class="move-back-btn"
            type="button"
            aria-label="Back"
            disabled={$browse.path.length === 0 || $view.busy}
            onclick={onBack}
        >
            <svg fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 19l-7-7 7-7" />
            </svg>
        </button>
        <div id="move-breadcrumb" class="move-breadcrumb">
            <button
                type="button"
                class="move-crumb"
                disabled={$browse.path.length === 0}
                onclick={() => onCrumb(-1)}
            >
                My Drive
            </button>
            {#each $browse.path as segment, idx (segment.id)}
                <span class="move-crumb-sep">/</span>
                <button
                    type="button"
                    class="move-crumb"
                    disabled={idx === $browse.path.length - 1}
                    onclick={() => onCrumb(idx)}
                >
                    {segment.name}
                </button>
            {/each}
        </div>
    </div>

    <div id="move-list" class="move-list">
        {#if $browse.listing.status === 'loading'}
            <div class="move-list-empty">Loading folders...</div>
        {:else if $browse.listing.folders.length === 0}
            <div class="move-list-empty">No folders here.</div>
        {:else}
            {#each $browse.listing.folders as folder (folder.id)}
                <button
                    type="button"
                    class={`move-item${$browse.blocked.has(folder.id) ? ' is-disabled' : ''}`}
                    disabled={$browse.blocked.has(folder.id)}
                    onclick={() => onOpenFolder(folder)}
                >
                    <span class="move-item-icon" aria-hidden="true">
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                            <path stroke-linecap="round" stroke-linejoin="round" d="M3 7a2 2 0 012-2h5l2 2h7a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2V7z" />
                        </svg>
                    </span>
                    <span class="move-item-name">{folder.name}</span>
                    <span class="move-item-arrow" aria-hidden="true">&gt;</span>
                </button>
            {/each}
        {/if}
    </div>

    {#if $view.error}
        <div id="move-error" class="modal-error">{$view.error}</div>
    {/if}

    {#snippet actions()}
        <button id="move-cancel" class="secondary-btn" type="button" disabled={$view.busy} onclick={close}>
            Cancel
        </button>
        <button id="move-confirm" class="primary-btn" type="button" disabled={confirmDisabled} onclick={confirm}>
            Move to "{currentName}"
        </button>
    {/snippet}
</ModalShell>
