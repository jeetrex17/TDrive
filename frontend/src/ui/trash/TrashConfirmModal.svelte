<script lang="ts">
    /**
     * The one question asked before anything leaves the trash for good. It sits
     * over the trash dialog; modal-a11y treats nested dialogs as one stack, so
     * this owns the keyboard, Escape and Android BACK while it is up.
     */
    import ModalShell from '../modals/ModalShell.svelte';
    import { trashConfirmModal, type TrashConfirmation } from './trash-confirm-store';

    interface Props {
        onConfirm: () => void | Promise<void>;
    }

    let { onConfirm }: Props = $props();

    const view = trashConfirmModal.state;
    const target = $derived($view.payload);

    function close(): void {
        if ($view.busy) return;
        trashConfirmModal.close();
    }

    function confirm(): void {
        if ($view.busy) return;
        // The controller closes this dialog before the async work starts.
        void onConfirm();
    }

    function title(confirmation: TrashConfirmation): string {
        return confirmation.kind === 'empty' ? 'Empty the trash?' : 'Delete permanently?';
    }

    function subtitle(confirmation: TrashConfirmation): string {
        if (confirmation.kind === 'empty') {
            return `This deletes ${confirmation.summary || 'everything in the trash'} from Telegram right away. It can't be undone.`;
        }
        return confirmation.isFolder
            ? "This deletes the folder and every file inside it from Telegram right away. It can't be undone."
            : "This deletes the file from Telegram right away. It can't be undone.";
    }
</script>

<ModalShell
    hostId="trash-confirm-modal"
    open={$view.open}
    title={target ? title(target) : ''}
    titleId="trash-confirm-title"
    subtitle={target ? subtitle(target) : ''}
    initialFocus="#trash-confirm-cancel"
    restoreFocus="#trash-empty"
    onClose={close}
>
    {#if target?.kind === 'purge'}
        <div class="trash-confirm-name" title={target.name}>{target.name}</div>
    {/if}

    {#snippet actions()}
        <button id="trash-confirm-cancel" class="secondary-btn" type="button" disabled={$view.busy} onclick={close}>
            Cancel
        </button>
        <button id="trash-confirm-accept" class="primary-btn danger-btn" type="button" disabled={$view.busy} onclick={confirm}>
            {target?.kind === 'empty' ? 'Empty trash' : 'Delete permanently'}
        </button>
    {/snippet}
</ModalShell>

<style>
    /* Matches the delete dialog's target line: the name, quoted by its frame. */
    .trash-confirm-name {
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
        padding: var(--space-2) var(--space-3);
        border: 1px solid var(--border);
        border-radius: var(--radius-md);
        background: var(--surface-control);
        font-size: var(--font-size-sm);
        font-weight: var(--weight-semibold);
    }

    :global(html.mobile) .trash-confirm-name {
        font-size: var(--mobile-type-body);
    }
</style>
