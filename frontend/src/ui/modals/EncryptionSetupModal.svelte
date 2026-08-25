<script lang="ts">
    import ModalShell from './ModalShell.svelte';
    import { encryptionSetupModal } from './encryption-setup-modal-store';

    interface Props {
        onCancel: () => void;
        onSubmit: (password: string, confirmPassword: string, hint: string) => void | Promise<void>;
    }

    let { onCancel, onSubmit }: Props = $props();
    let password = $state('');
    let confirmPassword = $state('');
    let passwordInput = $state<HTMLInputElement | null>(null);
    let confirmPasswordInput = $state<HTMLInputElement | null>(null);
    let hint = $state('');
    let wasOpen = false;

    const view = encryptionSetupModal.state;

    function clearPasswords(): void {
        password = '';
        confirmPassword = '';
        if (passwordInput) passwordInput.value = '';
        if (confirmPasswordInput) confirmPasswordInput.value = '';
    }

    function cancel(): void {
        if ($view.busy) return;
        clearPasswords();
        onCancel();
    }

    function submit(): void {
        if ($view.busy) return;
        const submittedPassword = password;
        const submittedConfirmation = confirmPassword;
        const submittedHint = hint;
        clearPasswords();
        void onSubmit(submittedPassword, submittedConfirmation, submittedHint);
    }

    $effect.pre(() => {
        if (!$view.open) {
            clearPasswords();
            hint = '';
            wasOpen = false;
            return;
        }
        if (!wasOpen) {
            clearPasswords();
            hint = '';
        }
        wasOpen = true;
    });
</script>

<ModalShell
    hostId="encryption-setup-modal"
    open={$view.open}
    title="Set an encryption password"
    titleId="encryption-setup-title"
    initialFocus="#encryption-setup-password"
    restoreFocus="#file-list"
    onClose={cancel}
>
    <p class="modal-subtitle">
        Use one password for every encrypted file in My Drive.
        <strong>If you forget it, encrypted files cannot be recovered.</strong>
    </p>
    <input
        id="encryption-setup-password"
        type="password"
        placeholder="Password"
        autocomplete="new-password"
        bind:this={passwordInput}
        bind:value={password}
        disabled={$view.busy}
    />
    <input
        id="encryption-setup-password-confirm"
        type="password"
        placeholder="Confirm password"
        autocomplete="new-password"
        bind:this={confirmPasswordInput}
        bind:value={confirmPassword}
        disabled={$view.busy}
    />
    <input
        id="encryption-setup-hint"
        type="text"
        placeholder="Optional hint (do not put the password here)"
        autocomplete="off"
        bind:value={hint}
        disabled={$view.busy}
    />
    <p class="field-help">
        This hint is shown when TDrive asks for your encryption password. It is not encrypted, so don't put the
        password itself here.
    </p>
    {#if $view.error}
        <div id="encryption-setup-error" class="modal-error">{$view.error}</div>
    {/if}

    {#snippet actions()}
        <button id="encryption-setup-cancel" class="secondary-btn" type="button" disabled={$view.busy} onclick={cancel}>
            Cancel
        </button>
        <button id="encryption-setup-confirm" class="primary-btn" type="button" disabled={$view.busy} onclick={submit}>
            Set password
        </button>
    {/snippet}
</ModalShell>
