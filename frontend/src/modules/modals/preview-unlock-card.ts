/**
 * The unlock card the preview shows in place of an encrypted photo.
 *
 * Password fallback and optional native biometrics share the current-session
 * unlock path. The card owns capability discovery and each visible request so
 * hiding, paging or closing cannot let old work change the next photo's card.
 *
 * The card deliberately does not take focus when it appears. While browsing,
 * the arrow keys should skip past a locked photo rather than be swallowed as
 * typing; the reader clicks the field when they actually want to unlock.
 */

import { getBiometricStatus, requireOperationSuccess, useBiometricUnlock, useEncryptionPassword, type OperationResult } from '../../api';
import type { BiometricStatusView } from '../../types';
import { biometricLabel } from '../../ui/modals/biometric-labels';
import { cancelBiometricAuthentication, waitForBiometricCancellation } from '../biometric';
import { humanizeBackendError } from '../errors';
import { loadEncryptionStatus } from '../encryption';

export interface UnlockCardElements {
    readonly card: HTMLElement | null;
    readonly input: HTMLInputElement | null;
    readonly unlockButton: HTMLButtonElement | null;
    readonly eyeButton: HTMLButtonElement | null;
    readonly error: HTMLElement | null;
    readonly hint: HTMLElement | null;
    readonly hintText: HTMLElement | null;
    readonly biometricButton?: HTMLButtonElement | null;
    readonly biometricLabel?: HTMLElement | null;
}

export interface UnlockCard {
    /** Reveal the card with this hint, or with no hint line if it is empty. */
    show(hint: string): void;
    /**
     * Hide the pill only. The frosted backdrop is the preview's to clear, when
     * an image or an error actually takes over, so that stepping between two
     * locked photos does not flash the gallery during the load in between.
     */
    hide(): void;
    /** Forget anything typed, for a close that must not leave a password behind. */
    clearInput(): void;
    toggleReveal(): void;
    /**
     * Try the typed password, then run `onUnlocked`.
     *
     * The callback is passed per submission rather than held on the card
     * because the preview has to decide which photo to reload *before* the
     * round trip starts: by the time it finishes the reader may have paged on,
     * and reloading whatever is showing then would drop them back on the photo
     * they had left.
     */
    submit(onUnlocked: () => void): Promise<void>;
    /** Authenticate only after the reader explicitly chooses the native action. */
    submitBiometric(onUnlocked: () => void): Promise<void>;
}

export function createUnlockCard(elements: UnlockCardElements): UnlockCard {
    const { card, input, unlockButton, eyeButton, error, hint, hintText, biometricButton, biometricLabel: label } = elements;
    let generation = 0;
    let visible = false;
    let busy: 'password' | 'biometric' | null = null;
    let biometric: BiometricStatusView | null = null;

    function isCurrent(request: number): boolean {
        return visible && generation === request && Boolean(card?.isConnected);
    }

    function setBusy(value: boolean): void {
        if (unlockButton) unlockButton.disabled = value;
        if (input) input.disabled = value;
        if (eyeButton) eyeButton.disabled = value;
        if (biometricButton) biometricButton.disabled = value;
        if (label) label.textContent = value && busy === 'biometric'
            ? 'Waiting for authentication…'
            : `Use ${biometricLabel(biometric?.kind ?? 'biometric')}`;
        card?.setAttribute('aria-busy', String(value));
    }

    function showError(message: string): void {
        if (!error) return;
        error.textContent = message;
        error.style.display = 'block';
    }

    function clearError(): void {
        if (!error) return;
        error.style.display = 'none';
        error.textContent = '';
    }

    function resetReveal(): void {
        if (input) input.type = 'password';
        if (!eyeButton) return;
        eyeButton.setAttribute('aria-pressed', 'false');
        eyeButton.setAttribute('aria-label', 'Show password');
    }

    function clearInput(): void {
        if (input) input.value = '';
        resetReveal();
    }

    function invalidate(): void {
        generation += 1;
        if (busy === 'biometric') cancelBiometricAuthentication();
        busy = null;
        biometric = null;
        setBusy(false);
        clearInput();
        if (biometricButton) biometricButton.hidden = true;
    }

    async function refreshBiometric(request: number): Promise<void> {
        if (!biometricButton) return;
        try {
            const status = await getBiometricStatus();
            if (!isCurrent(request)) return;
            biometric = status;
            biometricButton.hidden = !status.available || !status.enrolled;
            if (label) label.textContent = `Use ${biometricLabel(status.kind)}`;
        } catch {
            // Missing native capability leaves the password fallback usable.
            if (isCurrent(request)) biometricButton.hidden = true;
        }
    }

    async function unlock(mode: 'password' | 'biometric', call: () => Promise<OperationResult>, onUnlocked: () => void): Promise<void> {
        const request = generation;
        if (!isCurrent(request) || busy) return;
        busy = mode;
        clearInput();
        clearError();
        setBusy(true);
        try {
            if (mode === 'biometric') {
                await waitForBiometricCancellation();
                if (!isCurrent(request)) return;
            }
            const result = await call();
            if (!isCurrent(request)) return;
            requireOperationSuccess(result);
            await loadEncryptionStatus();
            if (!isCurrent(request)) return;
            busy = null;
            setBusy(false);
            window.dispatchEvent(new Event('tdrive:unlocked'));
            onUnlocked();
        } catch (err) {
            if (isCurrent(request)) showError(humanizeBackendError(err));
        } finally {
            if (isCurrent(request)) {
                busy = null;
                setBusy(false);
            }
        }
    }

    return {
        show(hintValue: string): void {
            if (!card) return;
            invalidate();
            visible = true;
            clearError();
            if (hint && hintText) {
                hintText.textContent = hintValue;
                hint.style.display = hintValue ? 'block' : 'none';
            }
            card.style.display = 'flex';
            void refreshBiometric(generation);
        },

        hide(): void {
            visible = false;
            invalidate();
            if (card) card.style.display = 'none';
        },

        clearInput,

        toggleReveal(): void {
            if (!input || !eyeButton || busy) return;
            const reveal = input.type === 'password';
            input.type = reveal ? 'text' : 'password';
            eyeButton.setAttribute('aria-pressed', reveal ? 'true' : 'false');
            eyeButton.setAttribute('aria-label', reveal ? 'Hide password' : 'Show password');
            // Toggling moves focus to the button, and the reader's next
            // keystroke belongs in the field they were typing in.
            try { input.focus(); } catch { /* focus is best-effort */ }
        },

        async submit(onUnlocked: () => void): Promise<void> {
            if (!isCurrent(generation) || busy) return;
            const value = String(input?.value || '');
            if (!value) {
                showError('Enter your encryption password.');
                return;
            }
            await unlock('password', () => useEncryptionPassword(value), onUnlocked);
        },

        async submitBiometric(onUnlocked: () => void): Promise<void> {
            if (!biometric?.available || !biometric.enrolled) return;
            await unlock('biometric', useBiometricUnlock, onUnlocked);
        },
    };
}
