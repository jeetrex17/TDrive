<script lang="ts">
    import ChevronLeftIcon from '@lucide/svelte/icons/chevron-left';
    import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
    import FolderIcon from '@lucide/svelte/icons/folder';
    import ModalShell from './ModalShell.svelte';
    import { isMobilePlatform } from '../../api';
    import { sidebarState } from '../sidebar/sidebar-store';
    import { pushSheet } from './sheet-stack';
    import { moveBrowse, moveModal, type MoveFolderEntry } from './move-modal-store';

    interface Props {
        onOpenFolder: (entry: MoveFolderEntry) => void;
        // crumbIndex -1 targets the drive root, otherwise a path index.
        onCrumb: (crumbIndex: number) => void;
        onBack: () => void;
        onConfirm: () => void | Promise<void>;
    }

    let { onOpenFolder, onCrumb, onBack, onConfirm }: Props = $props();

    const view = moveModal.state;
    const browse = moveBrowse;

    // The root is the active drive, named: a shared drive is not "My Drive".
    const rootName = $derived.by(() => {
        const { personal, shared, activeChannelId } = $sidebarState;
        const active = [...personal, ...shared].find((drive) => drive.id === activeChannelId);
        return active?.title?.trim() || 'My Drive';
    });

    const currentId = $derived($browse.path[$browse.path.length - 1]?.id ?? '');
    // The phone sheet shows the destination in its breadcrumb, so its button
    // is the short verb; the desktop dialog keeps naming the target folder.
    const currentName = $derived($browse.path[$browse.path.length - 1]?.name ?? rootName);
    const confirmLabel = $derived(isMobilePlatform() ? 'Move here' : `Move to "${currentName}"`);
    // Why the move is blocked, for helper text the button points at. Busy needs
    // no reason: the spinner is the reason.
    const disabledReason = $derived.by(() => {
        if ($view.busy) return '';
        if (currentId === $browse.sourceParent) return 'The items are already in this folder.';
        if ($browse.blocked.has(currentId)) return "You can't move a folder into itself.";
        return '';
    });
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

    // On a phone, hardware Back walks up one folder before it leaves the sheet,
    // the way Back works inside any drill-down. The entry sits above the dialog's
    // own Back claim, so it is consumed first; at the root there is none and Back
    // closes the sheet. The scrim, swipe and Cancel still dismiss outright.
    $effect(() => {
        if (!isMobilePlatform() || !$view.open || $browse.path.length === 0) return;
        const handle = pushSheet(() => onBack());
        return () => handle.release();
    });
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
                {rootName}
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
            <div class="move-list-empty">Loading folders…</div>
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
                    {#if $browse.blocked.has(folder.id)}
                        <span class="move-item-reason">Can't move here</span>
                    {:else}
                        <ChevronRightIcon class="move-item-arrow" size={16} strokeWidth={2} aria-hidden="true" />
                    {/if}
                </button>
            {/each}
        {/if}
    </div>

    {#if $view.error}
        <div id="move-error" class="modal-error" role="alert">{$view.error}</div>
    {:else if disabledReason}
        <p id="move-disabled-reason" class="move-disabled-reason">{disabledReason}</p>
    {/if}

    {#snippet actions()}
        <button id="move-cancel" class="secondary-btn" type="button" disabled={$view.busy} onclick={close}>
            Cancel
        </button>
        <button
            id="move-confirm"
            class="primary-btn"
            type="button"
            disabled={confirmDisabled}
            aria-describedby={disabledReason ? 'move-disabled-reason' : undefined}
            onclick={confirm}
        >
            {confirmLabel}
        </button>
    {/snippet}
</ModalShell>
