<script lang="ts">
    import ModalShell from './ModalShell.svelte';
    import { joinDriveModal } from './join-drive-modal-store';

    interface Props {
        onSubmit: (inviteLink: string) => void | Promise<void>;
    }

    let { onSubmit }: Props = $props();
    let link = $state('');
    let wasOpen = false;

    const view = joinDriveModal.state;

    function close(): void {
        if ($view.busy) return;
        joinDriveModal.close();
    }

    function submit(): void {
        const next = link.trim();
        if (!next || $view.busy) return;
        void onSubmit(next);
    }

    function onInputKeydown(event: KeyboardEvent): void {
        if (event.key === 'Enter') {
            event.preventDefault();
            submit();
        }
    }

    $effect(() => {
        if ($view.open && !wasOpen) {
            link = '';
        }
        wasOpen = $view.open;
    });
</script>

<ModalShell
    hostId="join-drive-modal"
    open={$view.open}
    title="Join shared drive"
    titleId="join-drive-title"
    subtitle="Paste the t.me invite link you received from a friend. If the drive has lots of history, joining may take a few seconds."
    initialFocus="#join-drive-link"
    restoreFocus="#drives-nav"
    onClose={close}
>
    <input
        id="join-drive-link"
        type="text"
        placeholder="https://t.me/+..."
        autocomplete="off"
        bind:value={link}
        disabled={$view.busy}
        onkeydown={onInputKeydown}
    />

    {#snippet actions()}
        <button id="join-drive-cancel" class="secondary-btn" type="button" disabled={$view.busy} onclick={close}>
            Cancel
        </button>
        <button
            id="join-drive-go"
            class="primary-btn"
            type="button"
            disabled={$view.busy || !link.trim()}
            onclick={submit}
        >
            Join
        </button>
    {/snippet}
</ModalShell>
