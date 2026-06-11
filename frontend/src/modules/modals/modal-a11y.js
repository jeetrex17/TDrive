// Keyboard + focus handling shared by the confirm dialogs.
//
// Wire it once in a modal's setup() with the modal element, its close callback,
// and the element to focus on open (use the safe / Cancel control for
// destructive modals). Call the returned activate() when the modal opens and
// deactivate() when it closes.
//
// Provides: focus the safe control on open, trap Tab inside the dialog, close
// on Escape (and stop the global Esc handler from firing behind the overlay),
// and restore focus to whatever was focused before the modal opened.

const FOCUSABLE = [
    'a[href]',
    'button:not([disabled])',
    'input:not([disabled])',
    'select:not([disabled])',
    'textarea:not([disabled])',
    '[tabindex]:not([tabindex="-1"])',
].join(',');

export function installModalA11y(modal, { requestClose, initialFocus } = {}) {
    let lastActive = null;

    const focusable = () =>
        Array.from(modal.querySelectorAll(FOCUSABLE)).filter((el) => el.offsetParent !== null);

    const onKeydown = (e) => {
        if (e.key === 'Escape') {
            e.preventDefault();
            e.stopPropagation();
            requestClose?.();
            return;
        }
        if (e.key !== 'Tab') return;
        const items = focusable();
        if (items.length === 0) return;
        const first = items[0];
        const last = items[items.length - 1];
        if (e.shiftKey && document.activeElement === first) {
            e.preventDefault();
            last.focus();
        } else if (!e.shiftKey && document.activeElement === last) {
            e.preventDefault();
            first.focus();
        }
    };

    return {
        activate() {
            lastActive = document.activeElement;
            modal.addEventListener('keydown', onKeydown);
            requestAnimationFrame(() => {
                const target =
                    (typeof initialFocus === 'function' ? initialFocus() : initialFocus) || focusable()[0];
                target?.focus();
            });
        },
        deactivate() {
            modal.removeEventListener('keydown', onKeydown);
            const restore = lastActive;
            lastActive = null;
            if (restore && typeof restore.focus === 'function') restore.focus();
        },
    };
}
