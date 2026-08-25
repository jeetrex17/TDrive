<script lang="ts">
    import ModalShell from './ModalShell.svelte';
    import { encryptionSettingsModal } from './encryption-settings-modal-store';

    interface Props {
        onCancel: () => void;
        onSubmit: (currentPassword: string, newPassword: string, confirmPassword: string, hint: string) => void | Promise<void>;
    }

    let { onCancel, onSubmit }: Props = $props();
    let currentPassword = $state('');
    let newPassword = $state('');
    let confirmPassword = $state('');
    let currentPasswordInput = $state<HTMLInputElement | null>(null);
    let newPasswordInput = $state<HTMLInputElement | null>(null);
    let confirmPasswordInput = $state<HTMLInputElement | null>(null);
    let hint = $state('');
    let revealCurrent = $state(false);
    let revealNew = $state(false);
    let wasOpen = false;

    const view = encryptionSettingsModal.state;

    function clearPasswords(): void {
        currentPassword = '';
        newPassword = '';
        confirmPassword = '';
        if (currentPasswordInput) currentPasswordInput.value = '';
        if (newPasswordInput) newPasswordInput.value = '';
        if (confirmPasswordInput) confirmPasswordInput.value = '';
        revealCurrent = false;
        revealNew = false;
    }

    function cancel(): void {
        if ($view.busy) return;
        clearPasswords();
        onCancel();
    }

    function submit(): void {
        if ($view.busy) return;
        const submittedCurrentPassword = currentPassword;
        const submittedNewPassword = newPassword;
        const submittedConfirmation = confirmPassword;
        const submittedHint = hint;
        clearPasswords();
        void onSubmit(submittedCurrentPassword, submittedNewPassword, submittedConfirmation, submittedHint);
    }

    // Hold-to-show: the input stays masked except while the eye button is
    // actively pressed (pointer or Space/Enter held).
    function holdHandlers(set: (visible: boolean) => void) {
        return {
            onpointerdown: (event: PointerEvent) => {
                event.preventDefault();
                set(true);
            },
            onpointerup: () => set(false),
            onpointerleave: () => set(false),
            onpointercancel: () => set(false),
            onblur: () => set(false),
            onkeydown: (event: KeyboardEvent) => {
                if (event.key === ' ' || event.key === 'Enter') {
                    event.preventDefault();
                    set(true);
                }
            },
            onkeyup: () => set(false),
        };
    }

    const currentHold = holdHandlers((visible) => (revealCurrent = visible));
    const newHold = holdHandlers((visible) => (revealNew = visible));

    $effect.pre(() => {
        if (!$view.open) {
            clearPasswords();
            hint = '';
            wasOpen = false;
            return;
        }
        if (!wasOpen) {
            clearPasswords();
            hint = $view.payload?.hint ?? '';
        }
        wasOpen = true;
    });
</script>

<ModalShell
    hostId="encryption-settings-modal"
    open={$view.open}
    title="Encryption settings"
    titleId="encryption-settings-title"
    subtitle="Change your encryption password safely. There is no recovery reset without the current password."
    initialFocus="#encryption-current-password"
    restoreFocus="#profile-trigger"
    onClose={cancel}
>
    <div class="input-with-action">
        <input
            id="encryption-current-password"
            type={revealCurrent ? 'text' : 'password'}
            placeholder="Current password"
            autocomplete="current-password"
            bind:this={currentPasswordInput}
            bind:value={currentPassword}
            disabled={$view.busy}
        />
        <button
            class="input-action-btn reveal-on-hold"
            type="button"
            data-state={revealCurrent ? 'visible' : undefined}
            disabled={$view.busy}
            aria-label="Hold to show current password"
            title="Hold to show password"
            {...currentHold}
        >
            <svg class="input-action-icon icon-eye" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M2.458 12C3.732 7.943 7.523 5 12 5c4.477 0 8.268 2.943 9.542 7-1.274 4.057-5.065 7-9.542 7-4.477 0-8.268-2.943-9.542-7z"/><path stroke-linecap="round" stroke-linejoin="round" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"/></svg>
        </button>
    </div>
    <div class="input-with-action">
        <input
            id="encryption-new-password"
            type={revealNew ? 'text' : 'password'}
            placeholder="New password"
            autocomplete="new-password"
            bind:this={newPasswordInput}
            bind:value={newPassword}
            disabled={$view.busy}
        />
        <button
            class="input-action-btn reveal-on-hold"
            type="button"
            data-state={revealNew ? 'visible' : undefined}
            disabled={$view.busy}
            aria-label="Hold to show new password"
            title="Hold to show password"
            {...newHold}
        >
            <svg class="input-action-icon icon-eye" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M2.458 12C3.732 7.943 7.523 5 12 5c4.477 0 8.268 2.943 9.542 7-1.274 4.057-5.065 7-9.542 7-4.477 0-8.268-2.943-9.542-7z"/><path stroke-linecap="round" stroke-linejoin="round" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"/></svg>
        </button>
    </div>
    <input
        id="encryption-new-password-confirm"
        type="password"
        placeholder="Confirm new password"
        autocomplete="new-password"
        bind:this={confirmPasswordInput}
        bind:value={confirmPassword}
        disabled={$view.busy}
    />
    <input
        id="encryption-settings-hint"
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
        <div id="encryption-settings-error" class="modal-error">{$view.error}</div>
    {/if}

    {#snippet actions()}
        <button id="encryption-settings-cancel" class="secondary-btn" type="button" disabled={$view.busy} onclick={cancel}>
            Cancel
        </button>
        <button id="encryption-settings-confirm" class="primary-btn" type="button" disabled={$view.busy} onclick={submit}>
            Save
        </button>
    {/snippet}
</ModalShell>
