<script lang="ts">
    import ModalShell from './ModalShell.svelte';
    import { closeLeaveDriveModalView, leaveDriveModalState, type LeaveDriveTarget } from './leave-drive-modal-store';

    interface Props {
        onConfirm: (target: LeaveDriveTarget) => void | Promise<void>;
    }

    let { onConfirm }: Props = $props();

    const subtitle = $derived.by(() => {
        const title = $leaveDriveModalState.target?.title || '';
        return title
            ? `Leave "${title}"? You can rejoin later with the invite link.`
            : 'You can rejoin later with the invite link.';
    });

    function canClose(): boolean {
        return !$leaveDriveModalState.inFlight;
    }

    function close(): void {
        if (!canClose()) return;
        closeLeaveDriveModalView();
    }

    function confirm(): void {
        const target = $leaveDriveModalState.target;
        if (!target || $leaveDriveModalState.inFlight) return;
        void onConfirm(target);
    }
</script>

<ModalShell
    hostId="leave-drive-modal"
    open={$leaveDriveModalState.open}
    title="Leave drive?"
    titleId="leave-drive-title"
    {subtitle}
    initialFocus="#leave-drive-cancel"
    restoreFocus="#drives-nav"
    onClose={close}
>
    {#snippet actions()}
        <button
            id="leave-drive-cancel"
            class="secondary-btn"
            type="button"
            disabled={$leaveDriveModalState.inFlight}
            onclick={close}
        >
            Cancel
        </button>
        <button
            id="leave-drive-confirm"
            class="primary-btn danger"
            type="button"
            disabled={$leaveDriveModalState.inFlight}
            onclick={confirm}
        >
            Leave
        </button>
    {/snippet}
</ModalShell>
