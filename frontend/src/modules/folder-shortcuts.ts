import { get } from 'svelte/store';
import { isMobilePlatform } from '../api';
import { state } from '../state';
import { isApplePlatform } from '../utils';
import { requestBreadcrumbEdit } from '../ui/chrome/breadcrumb-store';
import { openChannelKey } from '../ui/channels/channel-store';
import { contextMenuState } from '../ui/menus/context-menu-store';
import { hasActiveModal } from '../ui/modals/modal-a11y';
import { navigateBack, navigateHistory } from './navigation';

function isVisible(element: HTMLElement): boolean {
    for (let current: HTMLElement | null = element; current; current = current.parentElement) {
        const style = getComputedStyle(current);
        if (current.hidden || current.inert || style.display === 'none' || style.visibility === 'hidden') return false;
    }
    return true;
}

// The folder view is in front: no overlay, menu, drive switch or other view.
function folderViewOwnsInput(): boolean {
    if (hasActiveModal() || get(contextMenuState).open || state.channelSwitchInProgress
        || state.virtualView !== null || get(openChannelKey) !== null) return false;
    return ![...document.querySelectorAll<HTMLElement>('[role="menu"], [role="dialog"], [role="alertdialog"]')].some(isVisible);
}

function handleFolderShortcut(event: KeyboardEvent): void {
    if (event.defaultPrevented || event.repeat || event.isComposing || isMobilePlatform()) return;
    const parentArrow = event.key === 'ArrowUp'
        && ((event.altKey && !event.metaKey) || (isApplePlatform() && event.metaKey && !event.altKey));
    const parentBackspace = event.key === 'Backspace' && !event.altKey && !event.metaKey && !isApplePlatform();
    const parent = !event.ctrlKey && !event.shiftKey && (parentArrow || parentBackspace);
    const search = event.key.toLowerCase() === 'f' && !event.altKey && !event.shiftKey
        && (event.ctrlKey !== event.metaKey);
    // File Explorer's Ctrl+L: type or paste a path into the breadcrumb.
    const editPath = event.key.toLowerCase() === 'l' && !event.altKey && !event.shiftKey
        && (event.ctrlKey !== event.metaKey);
    const history = historyStep(event);
    if (!parent && !search && !editPath && history === 0) return;
    if (!folderViewOwnsInput()) return;

    const target = event.target instanceof Element ? event.target : null;
    // The breadcrumb's path field counts as one editor with its message.
    const editor = target?.closest('input, textarea, select, [contenteditable]:not([contenteditable="false"]), [role="textbox"], .breadcrumb-path-editor');
    const input = document.getElementById('search-input');
    if (editor && (!(search || editPath) || editor !== input)) return;

    if (search) {
        if (!(input instanceof HTMLInputElement) || input.disabled || !isVisible(input)) return;
        event.preventDefault();
        input.focus();
        input.select();
        return;
    }
    if (editPath) {
        event.preventDefault();
        requestBreadcrumbEdit();
        return;
    }
    if (state.searchQuery.trim()) return;
    if (history !== 0) {
        event.preventDefault();
        void navigateHistory(history);
        return;
    }
    if (state.folderPath.length === 0) return;
    event.preventDefault();
    navigateBack();
}

// File Explorer's Alt+Left / Alt+Right, and Finder's Cmd+[ / Cmd+] on Apple.
function historyStep(event: KeyboardEvent): -1 | 0 | 1 {
    if (event.ctrlKey || event.shiftKey) return 0;
    if (isApplePlatform()) {
        if (!event.metaKey || event.altKey) return 0;
        return event.key === '[' ? -1 : event.key === ']' ? 1 : 0;
    }
    if (!event.altKey || event.metaKey) return 0;
    return event.key === 'ArrowLeft' ? -1 : event.key === 'ArrowRight' ? 1 : 0;
}

// A mouse's side buttons step back (button 3) and forward (button 4), as in
// File Explorer. Handling mouseup also stops the webview's own history step.
function handleHistoryButton(event: MouseEvent): void {
    const step = event.button === 3 ? -1 : event.button === 4 ? 1 : 0;
    if (step === 0 || event.defaultPrevented || isMobilePlatform()) return;
    event.preventDefault();
    if (!folderViewOwnsInput() || state.searchQuery.trim()) return;
    void navigateHistory(step);
}

/** Dashboard-owned shortcuts also work when a folder is empty or the toolbar has focus. */
export function activateFolderShortcuts(): () => void {
    window.addEventListener('keydown', handleFolderShortcut);
    window.addEventListener('mouseup', handleHistoryButton);
    return () => {
        window.removeEventListener('keydown', handleFolderShortcut);
        window.removeEventListener('mouseup', handleHistoryButton);
    };
}
