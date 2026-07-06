<script lang="ts">
    import ModalShell from './ModalShell.svelte';
    import { logoutModal, type LogoutMode } from './logout-modal-store';

    interface Props {
        onConfirm: (mode: LogoutMode) => void | Promise<void>;
    }

    let { onConfirm }: Props = $props();
    let mode = $state<LogoutMode>('soft');
    let wasOpen = false;

    const view = logoutModal.state;

    function close(): void {
        if ($view.busy) return;
        logoutModal.close();
    }

    function confirm(): void {
        if ($view.busy) return;
        void onConfirm(mode);
    }

    $effect(() => {
        if ($view.open && !wasOpen) {
            mode = 'soft';
        }
        wasOpen = $view.open;
    });
</script>

<ModalShell
    hostId="logout-modal"
    open={$view.open}
    title="Log out of TDrive?"
    titleId="logout-title"
    subtitle="Pick how much to clear from this device."
    initialFocus="#logout-cancel"
    restoreFocus="#profile-trigger"
    onClose={close}
>
    <label class="modal-choice-row">
        <input type="radio" name="logout-mode" value="soft" bind:group={mode} disabled={$view.busy} />
        <span>
            <strong>Quick logout</strong>
            <small>Sign out but keep your local cache. Faster to re-login if it's still you.</small>
        </span>
    </label>
    <label class="modal-choice-row">
        <input type="radio" name="logout-mode" value="full" bind:group={mode} disabled={$view.busy} />
        <span>
            <strong>Log out and reset</strong>
            <small>Sign out everywhere and delete TDrive data on this device, including saved API credentials.</small>
        </span>
    </label>

    {#snippet actions()}
        <button id="logout-cancel" class="secondary-btn" type="button" disabled={$view.busy} onclick={close}>
            Cancel
        </button>
        <button id="logout-confirm" class="primary-btn danger" type="button" disabled={$view.busy} onclick={confirm}>
            Log out
        </button>
    {/snippet}
</ModalShell>
