import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { state } from '../state';
import { openChannelKey } from '../ui/channels/channel-store';
import { hideContextMenu, showContextMenu } from '../ui/menus/context-menu-store';
import { activateModalOwnership, deactivateModalOwnership } from '../ui/modals/modal-a11y';
import { activateFolderShortcuts } from './folder-shortcuts';

const actions = vi.hoisted(() => ({ navigateBack: vi.fn(), mobile: false, apple: false }));
vi.mock('./navigation', () => ({ navigateBack: actions.navigateBack }));
vi.mock('../api', () => ({ isMobilePlatform: () => actions.mobile }));
vi.mock('../utils', () => ({ isApplePlatform: () => actions.apple }));
let dispose: () => void;
function press(target: EventTarget = document.body, overrides: KeyboardEventInit = {}) {
    const event = new KeyboardEvent('keydown', { key: 'ArrowUp', altKey: true, bubbles: true, cancelable: true, ...overrides });
    target.dispatchEvent(event);
    return event;
}
const parentShortcuts = [
    { name: 'Alt+Up', keys: { key: 'ArrowUp', altKey: true } },
    { name: 'Backspace', keys: { key: 'Backspace', altKey: false } },
];

beforeEach(() => {
    vi.clearAllMocks();
    actions.mobile = false;
    actions.apple = false;
    state.folderPath = [{ id: 'folder', name: 'Folder' }];
    state.virtualView = null;
    state.searchQuery = '';
    state.channelSwitchInProgress = false;
    openChannelKey.set(null);
    hideContextMenu();
    document.body.innerHTML = '<button id="toolbar">Toolbar</button><input id="search-input"><div id="file-list"></div>';
    dispose = activateFolderShortcuts();
});
afterEach(() => {
    dispose();
    document.body.replaceChildren();
});

describe('folder shortcuts', () => {
    it.each(parentShortcuts)('navigates with $name from body, toolbar and an empty folder', ({ keys }) => {
        for (const target of [document.body, document.getElementById('toolbar')!, document.getElementById('file-list')!]) {
            expect(press(target, keys).defaultPrevented).toBe(true);
        }
        expect(actions.navigateBack).toHaveBeenCalledTimes(3);
    });
    it('accepts Cmd+Up only on Apple platforms and Alt+Up on both', () => {
        press(document.body, { altKey: false, metaKey: true });
        expect(actions.navigateBack).not.toHaveBeenCalled();
        actions.apple = true;
        press(document.body, { altKey: false, metaKey: true });
        press();
        expect(actions.navigateBack).toHaveBeenCalledTimes(2);
    });
    it('leaves Backspace available for row deletion on Apple platforms', () => {
        actions.apple = true;
        expect(press(document.body, { key: 'Backspace', altKey: false }).defaultPrevented).toBe(false);
        expect(actions.navigateBack).not.toHaveBeenCalled();
    });
    it.each(parentShortcuts)('leaves editing, composition, repeats and other modifiers alone for $name', ({ keys }) => {
        press(document.getElementById('search-input')!, keys);
        const editor = document.createElement('div');
        editor.contentEditable = 'true';
        document.body.append(editor);
        press(editor, keys);
        for (const extra of [{ ctrlKey: true }, { shiftKey: true }, { metaKey: true }, { repeat: true }, { isComposing: true }]) {
            press(document.body, { ...keys, ...extra });
        }
        expect(actions.navigateBack).not.toHaveBeenCalled();
    });
    it.each(parentShortcuts)('ignores root, alternate views, search, drive switching and mobile for $name', ({ keys }) => {
        state.folderPath = [];
        press(document.body, keys);
        state.folderPath = [{ id: 'folder', name: 'Folder' }];
        state.virtualView = 'photos';
        press(document.body, keys);
        state.virtualView = 'trash';
        press(document.body, keys);
        state.virtualView = null;
        state.searchQuery = 'query';
        press(document.body, keys);
        state.searchQuery = '';
        state.channelSwitchInProgress = true;
        press(document.body, keys);
        state.channelSwitchInProgress = false;
        openChannelKey.set('channel');
        press(document.body, keys);
        openChannelKey.set(null);
        actions.mobile = true;
        press(document.body, keys);
        expect(actions.navigateBack).not.toHaveBeenCalled();
    });
    it('leaves modal and menu ownership intact', () => {
        const modal = document.createElement('div');
        document.body.append(modal);
        activateModalOwnership(modal);
        press();
        deactivateModalOwnership(modal);
        showContextMenu(0, 0, []);
        press();
        hideContextMenu();
        expect(actions.navigateBack).not.toHaveBeenCalled();
    });
    it('leaves unregistered profile dialogs alone until hidden', () => {
        const panel = document.createElement('div');
        panel.setAttribute('role', 'dialog');
        panel.tabIndex = -1;
        document.body.append(panel);
        panel.focus();
        press(panel);
        press(panel, { key: 'f', altKey: false, ctrlKey: true });
        expect(actions.navigateBack).not.toHaveBeenCalled();
        expect(document.activeElement).toBe(panel);

        panel.hidden = true;
        press();
        expect(actions.navigateBack).toHaveBeenCalledTimes(1);
        press(document.body, { key: 'f', altKey: false, metaKey: true });
        expect(document.activeElement).toBe(document.getElementById('search-input'));
    });
    it.each(['Control', 'Command'])('focuses search with %s+F while leaving another editor alone', (modifier) => {
        const input = document.getElementById('search-input') as HTMLInputElement;
        input.value = 'previous query';
        const search = { key: 'f', altKey: false, ctrlKey: modifier === 'Control', metaKey: modifier === 'Command' };
        expect(press(document.body, search).defaultPrevented).toBe(true);
        expect(document.activeElement).toBe(input);
        expect(input.selectionStart).toBe(0);
        expect(input.selectionEnd).toBe(input.value.length);
        const editor = document.createElement('textarea');
        document.body.append(editor);
        editor.focus();
        press(editor, search);
        expect(document.activeElement).toBe(editor);
        input.hidden = true;
        document.getElementById('toolbar')!.focus();
        press(document.body, search);
        expect(document.activeElement).toBe(document.getElementById('toolbar'));
    });
    it('does not handle prevented events and removes its listener on disposal', () => {
        const prevent = (event: Event) => event.preventDefault();
        document.body.addEventListener('keydown', prevent);
        press();
        document.body.removeEventListener('keydown', prevent);
        dispose();
        press();
        expect(actions.navigateBack).not.toHaveBeenCalled();
    });
});
