/**
 * The unlock card the preview shows in place of an encrypted photo.
 *
 * It is a password field, a reveal toggle, an error line and a hint, and it
 * knows exactly one thing about the preview: that something should reload once
 * the drive is unlocked. Keeping it here rather than in the preview controller
 * is what stops its six elements from being six more nullable module variables
 * in a file that already had twenty-five, and it is why its rules -- the field
 * clears on every reopen, the reveal toggle starts hidden, both controls are
 * disabled while the password is in flight -- can be tested without standing up
 * a preview, a rendition broker and a gallery first.
 *
 * The card deliberately does not take focus when it appears. While browsing,
 * the arrow keys should skip past a locked photo rather than be swallowed as
 * typing; the reader clicks the field when they actually want to unlock.
 */

import { requireOperationSuccess, useEncryptionPassword } from '../../api';
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
}

export function createUnlockCard(elements: UnlockCardElements): UnlockCard {
    const { card, input, unlockButton, eyeButton, error, hint, hintText } = elements;

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

    return {
        show(hintValue: string): void {
            if (!card) return;
            clearError();
            if (input) input.value = '';
            resetReveal();
            if (hint && hintText) {
                hintText.textContent = hintValue;
                hint.style.display = hintValue ? 'block' : 'none';
            }
            card.style.display = 'flex';
        },

        hide(): void {
            if (card) card.style.display = 'none';
        },

        clearInput(): void {
            if (input) input.value = '';
        },

        toggleReveal(): void {
            if (!input || !eyeButton) return;
            const reveal = input.type === 'password';
            input.type = reveal ? 'text' : 'password';
            eyeButton.setAttribute('aria-pressed', reveal ? 'true' : 'false');
            eyeButton.setAttribute('aria-label', reveal ? 'Hide password' : 'Show password');
            // Toggling moves focus to the button, and the reader's next
            // keystroke belongs in the field they were typing in.
            try { input.focus(); } catch { /* focus is best-effort */ }
        },

        async submit(onUnlocked: () => void): Promise<void> {
            const value = String(input?.value || '');
            if (!value) {
                showError('Enter your encryption password.');
                return;
            }
            // Both controls go dead for the round trip, because a second
            // submission would race the first and report its failure over the
            // success of the one that worked.
            if (unlockButton) unlockButton.disabled = true;
            if (input) input.disabled = true;
            try {
                // A wrong password comes back as an unsuccessful result rather
                // than a rejection, so without this the card would report the
                // unlock as done and the photo would stay locked.
                requireOperationSuccess(await useEncryptionPassword(value));
                await loadEncryptionStatus();
                if (input) input.value = '';
                // The gallery's locked thumbnail cells are someone else's
                // problem, and they are listening for this.
                window.dispatchEvent(new Event('tdrive:unlocked'));
                onUnlocked();
            } catch (err) {
                showError(humanizeBackendError(err));
            } finally {
                if (unlockButton) unlockButton.disabled = false;
                if (input) input.disabled = false;
            }
        },
    };
}
