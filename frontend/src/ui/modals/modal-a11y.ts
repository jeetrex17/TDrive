// Keyboard, focus, and overlay ownership shared by ModalShell-based dialogs.
// The active stack has one keyboard owner: the topmost dialog. While it is
// open, every other branch of the application is inert and hidden from the
// accessibility tree. This also makes nested dialogs behave as one stack.
//
// Android's BACK is the same press as Escape, so it is answered from here
// rather than by each dialog: anything that installs a close path gets BACK
// for free, and the two can never disagree about what is on top.

import { pushSheet, type SheetHandle } from './sheet-stack';

const FOCUSABLE = [
    'a[href]',
    'button:not([disabled])',
    'input:not([disabled])',
    'select:not([disabled])',
    'textarea:not([disabled])',
    '[tabindex]:not([tabindex="-1"])',
].join(',');

const activeModals: HTMLElement[] = [];

interface BackgroundState {
    inert: boolean;
    ariaHidden: string | null;
}

const backgroundStates = new Map<HTMLElement, BackgroundState>();


function rememberBackground(element: HTMLElement): void {
    if (backgroundStates.has(element)) return;
    backgroundStates.set(element, {
        inert: element.inert,
        ariaHidden: element.getAttribute('aria-hidden'),
    });
}

function hideBackground(element: HTMLElement): void {
    rememberBackground(element);
    element.inert = true;
    element.setAttribute('aria-hidden', 'true');
}

function restoreForeground(element: HTMLElement): void {
    const previous = backgroundStates.get(element);
    if (!previous) return;
    element.inert = previous.inert;
    if (element.classList.contains('modal-overlay') && activeModals.includes(element)) {
        element.setAttribute('aria-hidden', 'false');
    } else if (previous.ariaHidden === null) {
        element.removeAttribute('aria-hidden');
    } else {
        element.setAttribute('aria-hidden', previous.ariaHidden);
    }
}

function restoreAllBackgrounds(): void {
    for (const [element, previous] of backgroundStates) {
        element.inert = previous.inert;
        // Closed modal hosts must remain hidden even when they were exposed
        // before they entered the stack. ModalShell owns their display state.
        if (element.classList.contains('modal-overlay') && !activeModals.includes(element)) {
            element.setAttribute('aria-hidden', 'true');
            continue;
        }
        if (previous.ariaHidden === null) element.removeAttribute('aria-hidden');
        else element.setAttribute('aria-hidden', previous.ariaHidden);
    }
    backgroundStates.clear();
}

function syncModalOwnership(): void {
    const top = activeModals[activeModals.length - 1];
    if (!top) {
        restoreAllBackgrounds();
        return;
    }

    // Walk from the active host to <body>. At each level, every sibling branch
    // is background. This includes document portals such as notifications while
    // preserving the active dialog's ancestor path.
    let branch: HTMLElement | null = top;
    while (branch?.parentElement) {
        const parent: HTMLElement = branch.parentElement;
        for (const sibling of Array.from(parent.children)) {
            if (sibling instanceof HTMLElement && sibling !== branch) hideBackground(sibling);
        }
        restoreForeground(branch);
        branch = parent;
    }
}

export function activateModalOwnership(modal: HTMLElement): void {
    const existingIndex = activeModals.indexOf(modal);
    if (existingIndex >= 0) activeModals.splice(existingIndex, 1);
    activeModals.push(modal);
    syncModalOwnership();
}

export function deactivateModalOwnership(modal: HTMLElement): void {
    const index = activeModals.lastIndexOf(modal);
    if (index >= 0) activeModals.splice(index, 1);
    syncModalOwnership();
}

export function hasActiveModal(): boolean {
    return activeModals.length > 0;
}


function resolveTarget(target: unknown): Element | null {
    if (typeof target === 'function') return target();
    if (typeof target === 'string') return document.querySelector(target);
    return target instanceof Element ? target : null;
}

function canFocus(el: unknown): el is HTMLElement {
    return Boolean(
        el instanceof HTMLElement &&
        typeof el.focus === 'function' &&
        el.isConnected &&
        !el.hasAttribute('disabled') &&
        !el.closest('[inert], [aria-hidden="true"]') &&
        el.offsetParent !== null,
    );
}

function focusIfPossible(el: unknown): boolean {
    if (!canFocus(el)) return false;
    el.focus({ preventScroll: true });
    return document.activeElement === el;
}

export function installModalA11y(
    modal: HTMLElement,
    { requestClose, initialFocus, restoreFocus }: {
        requestClose?: () => void;
        initialFocus?: Element | (() => Element | null) | null;
        restoreFocus?: Element | string | (() => Element | null) | null;
    } = {},
) {
    let lastActive: Element | null = null;
    let active = false;
    let backEntry: SheetHandle | null = null;

    // Registers this dialog as the surface BACK dismisses next.
    const claimBack = (): void => {
        if (!requestClose || backEntry) return;
        backEntry = pushSheet(() => {
            // The stack has already dropped this entry.
            backEntry = null;
            requestClose();
            // A press can land on an inner layer instead -- the video player's
            // playlist, say -- and leave the dialog itself open. It is still
            // the surface the next press belongs to, so claim it again.
            if (active) claimBack();
        });
    };

    const focusable = (): HTMLElement[] =>
        Array.from(modal.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(canFocus);

    const onKeydown = (event: KeyboardEvent) => {
        if (activeModals[activeModals.length - 1] !== modal) return;
        if (event.key === 'Escape') {
            event.preventDefault();
            event.stopImmediatePropagation();
            requestClose?.();
            return;
        }
        if (event.key !== 'Tab') return;
        const items = focusable();
        if (items.length === 0) {
            event.preventDefault();
            return;
        }
        const first = items[0];
        const last = items[items.length - 1];
        if (!modal.contains(document.activeElement)) {
            event.preventDefault();
            first.focus({ preventScroll: true });
            return;
        }
        if (event.shiftKey && document.activeElement === first) {
            event.preventDefault();
            last.focus({ preventScroll: true });
        } else if (!event.shiftKey && document.activeElement === last) {
            event.preventDefault();
            first.focus({ preventScroll: true });
        }
    };

    return {
        activate() {
            if (active) return;
            active = true;
            lastActive = document.activeElement instanceof Element ? document.activeElement : null;
            activateModalOwnership(modal);
            claimBack();
            document.addEventListener('keydown', onKeydown, true);
            const target =
                (typeof initialFocus === 'function' ? initialFocus() : initialFocus) || focusable()[0];
            if (!focusIfPossible(target)) focusIfPossible(focusable()[0]);
        },
        deactivate() {
            if (!active) return;
            active = false;
            backEntry?.release();
            backEntry = null;
            document.removeEventListener('keydown', onKeydown, true);
            deactivateModalOwnership(modal);
            const restore = lastActive;
            lastActive = null;
            if (!focusIfPossible(restore)) focusIfPossible(resolveTarget(restoreFocus));
        },
    };
}
