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

function resolveTarget(target: any) {
    if (typeof target === 'function') return target();
    if (typeof target === 'string') return document.querySelector(target);
    return target || null;
}

function canFocus(el: any) {
    return Boolean(
        el &&
        typeof el.focus === 'function' &&
        el.isConnected &&
        !el.disabled &&
        el.offsetParent !== null
    );
}

function focusIfPossible(el: any) {
    if (!canFocus(el)) return false;
    el.focus({ preventScroll: true });
    return true;
}

export function installModalA11y(modal: any, { requestClose, initialFocus, restoreFocus }: { requestClose?: any; initialFocus?: any; restoreFocus?: any } = {}) {
    let lastActive: any = null;
    let active = false;

    const focusable = (): any[] =>
        Array.from(modal.querySelectorAll(FOCUSABLE)).filter(canFocus);

    const onKeydown = (e: any) => {
        if (e.key === 'Escape') {
            e.preventDefault();
            e.stopPropagation();
            requestClose?.();
            return;
        }
        if (e.key !== 'Tab') return;
        const items = focusable();
        if (items.length === 0) {
            e.preventDefault();
            return;
        }
        const first = items[0];
        const last = items[items.length - 1];
        if (!modal.contains(document.activeElement)) {
            e.preventDefault();
            first.focus();
            return;
        }
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
            if (active) return;
            active = true;
            lastActive = document.activeElement;
            document.addEventListener('keydown', onKeydown, true);
            requestAnimationFrame(() => {
                const target =
                    (typeof initialFocus === 'function' ? initialFocus() : initialFocus) || focusable()[0];
                if (!focusIfPossible(target)) {
                    focusIfPossible(focusable()[0]);
                }
            });
        },
        deactivate() {
            if (!active) return;
            active = false;
            document.removeEventListener('keydown', onKeydown, true);
            const restore = lastActive;
            lastActive = null;
            if (!focusIfPossible(restore)) {
                focusIfPossible(resolveTarget(restoreFocus));
            }
        },
    };
}
