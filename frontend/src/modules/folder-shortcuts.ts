import { get } from 'svelte/store';
import { isMobilePlatform } from '../api';
import { state } from '../state';
import { isApplePlatform } from '../utils';
import { openChannelKey } from '../ui/channels/channel-store';
import { contextMenuState } from '../ui/menus/context-menu-store';
import { hasActiveModal } from '../ui/modals/modal-a11y';
import { navigateBack } from './navigation';

function isVisible(element: HTMLElement): boolean {
    for (let current: HTMLElement | null = element; current; current = current.parentElement) {
        const style = getComputedStyle(current);
        if (current.hidden || current.inert || style.display === 'none' || style.visibility === 'hidden') return false;
    }
    return true;
}

function handleFolderShortcut(event: KeyboardEvent): void {
    if (event.defaultPrevented || event.repeat || event.isComposing || isMobilePlatform()) return;
    const parent = event.key === 'ArrowUp' && !event.ctrlKey && !event.shiftKey
        && ((event.altKey && !event.metaKey) || (isApplePlatform() && event.metaKey && !event.altKey));
    const search = event.key.toLowerCase() === 'f' && !event.altKey && !event.shiftKey
        && (event.ctrlKey !== event.metaKey);
    if (!parent && !search) return;
    if (hasActiveModal() || get(contextMenuState).open || state.channelSwitchInProgress
        || state.virtualView !== null || get(openChannelKey) !== null) return;
    if ([...document.querySelectorAll<HTMLElement>('[role="menu"], [role="dialog"], [role="alertdialog"]')].some(isVisible)) return;

    const target = event.target instanceof Element ? event.target : null;
    const editor = target?.closest('input, textarea, select, [contenteditable]:not([contenteditable="false"]), [role="textbox"]');
    const input = document.getElementById('search-input');
    if (editor && (!search || editor !== input)) return;

    if (search) {
        if (!(input instanceof HTMLInputElement) || input.disabled || !isVisible(input)) return;
        event.preventDefault();
        input.focus();
        input.select();
        return;
    }
    if (state.searchQuery.trim() || state.folderPath.length === 0) return;
    event.preventDefault();
    navigateBack();
}

/** Dashboard-owned shortcuts also work when a folder is empty or the toolbar has focus. */
export function activateFolderShortcuts(): () => void {
    window.addEventListener('keydown', handleFolderShortcut);
    return () => window.removeEventListener('keydown', handleFolderShortcut);
}
