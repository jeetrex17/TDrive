<script lang="ts">
    import ModalShell from './ModalShell.svelte';
    import { encryptionPasswordModal } from './encryption-password-modal-store';

    interface Props {
        onCancel: () => void;
        onSubmit: (password: string) => void | Promise<void>;
    }

    let { onCancel, onSubmit }: Props = $props();
    let password = $state('');
    let wasOpen = false;

    const view = encryptionPasswordModal.state;
    const hint = $derived(($view.payload?.hint ?? '').trim());

    function cancel(): void {
        if ($view.busy) return;
        onCancel();
    }

    function submit(): void {
        if ($view.busy) return;
        void onSubmit(password);
    }

    function onInputKeydown(event: KeyboardEvent): void {
        if (event.key === 'Enter') {
            event.preventDefault();
            submit();
        }
    }

    $effect(() => {
        if ($view.open && !wasOpen) {
            password = '';
        }
        wasOpen = $view.open;
    });
</script>

<ModalShell
    hostId="encryption-password-modal"
    open={$view.open}
    title="Enter encryption password"
    titleId="encryption-password-title"
    subtitle="Needed for encrypted uploads, downloads, and previews. TDrive remembers it until you close the app."
    initialFocus="#encryption-password-input"
    restoreFocus="#file-list"
    onClose={cancel}
>
    {#if hint}
        <p id="encryption-password-hint" class="modal-subtitle">
            Hint: <span id="encryption-password-hint-text">{hint}</span>
        </p>
    {/if}
    <input
        id="encryption-password-input"
        type="password"
        placeholder="Password"
        autocomplete="current-password"
        bind:value={password}
        disabled={$view.busy}
        onkeydown={onInputKeydown}
    />
    {#if $view.error}
        <div id="encryption-password-error" class="modal-error">{$view.error}</div>
    {/if}

    {#snippet actions()}
        <button id="encryption-password-cancel" class="secondary-btn" type="button" disabled={$view.busy} onclick={cancel}>
            Cancel
        </button>
        <button id="encryption-password-confirm" class="primary-btn" type="button" disabled={$view.busy} onclick={submit}>
            Continue
        </button>
    {/snippet}
</ModalShell>
