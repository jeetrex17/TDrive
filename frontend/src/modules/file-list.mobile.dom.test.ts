// The phone list's tap and press contract: a long press on a row opens the
// row's action sheet with the row named in its header; a press on the leading
// icon starts a selection; a tap toggles the row while a selection exists.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';

const api = vi.hoisted(() => ({
    isMobilePlatform: vi.fn(() => true),
    isIOSPlatform: () => false,
    isAndroidPlatform: () => false,
    playHaptic: vi.fn(),
}));
const actions = vi.hoisted(() => ({
    playVideo: vi.fn(),
    openFile: vi.fn(),
    triggerRefresh: vi.fn(),
    refreshFiles: vi.fn(),
    navigateToFolder: vi.fn(),
    enqueueDownload: vi.fn(),
}));

vi.mock('../api', () => api);
vi.mock('./app-actions', () => ({ appActions: () => actions }));
vi.mock('./navigation', () => ({ navigateToFolder: actions.navigateToFolder }));
vi.mock('./transfers', () => ({
    chooseFilesForCurrentFolder: vi.fn(),
    enqueueDownload: actions.enqueueDownload,
    enqueueFolderDownload: vi.fn(),
}));
vi.mock('./connectivity', () => ({ isOffline: () => false }));
vi.mock('./drag-drop', () => ({
    beginRowDrag: vi.fn(), endRowDrag: vi.fn(), canDropOnFolder: vi.fn(), setDropHighlight: vi.fn(), performDropMove: vi.fn(),
}));
vi.mock('./context-menu', () => ({ showRowContextMenu: vi.fn() }));
vi.mock('./modals/rename', () => ({ openRenameModal: vi.fn() }));
vi.mock('./modals/delete', () => ({ openDeleteModal: vi.fn() }));
vi.mock('./modals/folder', () => ({ openNewFolderModal: vi.fn() }));
vi.mock('./gallery', () => ({ renderGallery: vi.fn(), setPhotosMode: vi.fn() }));
vi.mock('./uploaders', () => ({ ensureUserNames: vi.fn(), uploaderChipLabel: () => null }));
vi.mock('./drive-data', () => ({ calculateVisibleFolderStats: vi.fn() }));
vi.mock('./folder-index', () => ({ refreshFolderIndex: vi.fn(), collectDescendants: vi.fn() }));

import FileList from '../ui/file-list/FileList.svelte';
import { contextMenuState } from '../ui/menus/context-menu-store';
import { showRowContextMenu } from './context-menu';
import { activateFileList, buildFileRow, buildFolderRow, renderFileListRows } from './file-list';
import { state } from '../state';

let list: HTMLElement;
let app: Record<string, unknown> | null = null;
let deactivate = () => {};

function press(target: Element, x = 120, y = 40): void {
    target.dispatchEvent(new PointerEvent('pointerdown', { pointerId: 1, pointerType: 'touch', isPrimary: true, clientX: x, clientY: y, bubbles: true }));
    vi.advanceTimersByTime(350);
    target.dispatchEvent(new PointerEvent('pointerup', { pointerId: 1, pointerType: 'touch', isPrimary: true, clientX: x, clientY: y, bubbles: true }));
    // Past the window in which the press swallows the click the browser
    // synthesises on release.
    vi.advanceTimersByTime(800);
}

function click(target: Element): void {
    target.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
}

function row(name: string): HTMLElement {
    const node = list.querySelector<HTMLElement>(`.drive-row[data-name="${name}"]`);
    if (!node) throw new Error(`missing row ${name}`);
    return node;
}

beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
    state.activeChannel = { id: 1, title: 'Drive', kind: 'personal' };
    state.selectedItems.clear();
    list = document.createElement('div');
    list.id = 'file-list';
    document.body.append(list);
    app = mount(FileList, { target: list });
    deactivate = activateFileList();
    renderFileListRows(list, [
        buildFolderRow({ id: 'design', name: 'Design' }, ''),
        buildFileRow({ id: 41, name: 'plan.pdf', size: 2_000_000, date: 1_700_000_000 }, ''),
        buildFileRow({ id: 42, name: 'photo.jpg', size: 3_000_000, date: 1_700_000_100 }, ''),
    ]);
    flushSync();
});

afterEach(async () => {
    deactivate();
    contextMenuState.set({ open: false, x: 0, y: 0, items: [], header: null, focusVersion: 0 });
    state.selectedItems.clear();
    if (app) await unmount(app);
    app = null;
    list.remove();
    vi.useRealTimers();
});

describe('phone file list', () => {
    it('long press opens the row action sheet and names the row in its header', () => {
        vi.mocked(showRowContextMenu).mockClear();

        press(row('plan.pdf').querySelector('.row-text')!, 130, 44);
        expect(showRowContextMenu).toHaveBeenCalledTimes(1);
        const [target, x, y, options] = vi.mocked(showRowContextMenu).mock.calls[0];
        expect(target).toBe(row('plan.pdf'));
        expect([x, y]).toEqual([130, 44]);
        expect(options?.header).toEqual({
            title: 'plan.pdf',
            // Type leads the meta line so the column reads the same on every row.
            meta: expect.stringMatching(/^PDF · 1\.9 MB · /),
            kind: 'file',
            ext: 'PDF',
            // The sheet's detail table. Location comes last so the reader ends
            // on where the file lives, which is what a move or rename changes.
            details: [
                { label: 'Type', value: 'PDF file', icon: 'type' },
                { label: 'Size', value: '1.9 MB', icon: 'size' },
                { label: 'Added', value: expect.any(String), icon: 'added' },
                { label: 'Location', value: expect.any(String), icon: 'location' },
            ],
        });
        expect(state.selectedItems.size).toBe(0);
    });

    it('the overflow button opens the same action sheet', () => {
        vi.mocked(showRowContextMenu).mockClear();
        click(row('plan.pdf').querySelector('button.row-more')!);
        expect(showRowContextMenu).toHaveBeenCalledTimes(1);
        expect(actions.openFile).not.toHaveBeenCalled();
    });

    it('the overflow click stops before document, so the sheet survives it', () => {
        // The menu dismisses itself on any click outside its own element. The
        // click that opens it would otherwise reach that handler in the same
        // tick, before the sheet has rendered, and close it again.
        const reachedDocument = vi.fn();
        document.addEventListener('click', reachedDocument);
        try {
            click(row('plan.pdf').querySelector('button.row-more')!);
            expect(showRowContextMenu).toHaveBeenCalled();
            expect(reachedDocument).not.toHaveBeenCalled();
        } finally {
            document.removeEventListener('click', reachedDocument);
        }
    });

    it('a press on the leading icon selects, then taps toggle instead of opening', () => {
        press(row('plan.pdf').querySelector('.file-type-icon')!);
        flushSync();
        expect(state.selectedItems.has('file:41')).toBe(true);
        expect(row('plan.pdf').classList.contains('is-selected')).toBe(true);

        click(row('photo.jpg').querySelector('.row-text')!);
        flushSync();
        expect(state.selectedItems.has('file:42')).toBe(true);
        expect(actions.openFile).not.toHaveBeenCalled();

        click(row('photo.jpg').querySelector('.row-text')!);
        click(row('plan.pdf').querySelector('.row-text')!);
        flushSync();
        expect(state.selectedItems.size).toBe(0);
    });

    it('a tap opens: folders push, viewer files open, other files download', () => {
        click(row('Design').querySelector('.row-text')!);
        expect(actions.navigateToFolder).toHaveBeenCalledWith('design', 'Design');

        click(row('plan.pdf').querySelector('.row-text')!);
        expect(actions.openFile).toHaveBeenCalledWith(expect.objectContaining({ id: 41, name: 'plan.pdf' }));

        row('plan.pdf').dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
        expect(actions.openFile).toHaveBeenCalledTimes(1);
    });
});
