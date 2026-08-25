<script lang="ts">
    import ChevronLeftIcon from '@lucide/svelte/icons/chevron-left';
    import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
    import FolderIcon from '@lucide/svelte/icons/folder';
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
            <ChevronLeftIcon size={16} strokeWidth={2} aria-hidden="true" />
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
                        <FolderIcon size={16} strokeWidth={2} aria-hidden="true" />
                    </span>
                    <span class="move-item-name">{folder.name}</span>
                    <ChevronRightIcon class="move-item-arrow" size={16} strokeWidth={2} aria-hidden="true" />
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
