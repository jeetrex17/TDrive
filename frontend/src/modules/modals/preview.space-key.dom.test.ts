// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { FileCommandItem } from '../../ui/file-list/types';

const actions = vi.hoisted(() => ({
    openFile: vi.fn(async () => {}),
    playVideo: vi.fn(async () => {}),
    refreshFiles: vi.fn(),
    triggerRefresh: vi.fn(async () => {}),
    downloadFile: vi.fn(),
}));
const notifications = vi.hoisted(() => ({ notify: vi.fn() }));

vi.mock('../app-actions', () => ({ appActions: () => actions }));
vi.mock('../notifications', () => notifications);
vi.mock('../../api', () => ({
    getPreviewFile: vi.fn(),
    getPreviewThumbnail: vi.fn(),
    hasOperationErrorCode: vi.fn(),
    isMobilePlatform: vi.fn(() => false),
    onRuntimeEvent: vi.fn(() => vi.fn()),
    openExternalUrl: vi.fn(),
    useEncryptionPassword: vi.fn(),
    closeMedia: vi.fn(),
    openOriginalImage: vi.fn(async (id: number) => ({ token: `image-${id}`, url: `http://127.0.0.1/image-${id}` })),
}));
vi.mock('../renditions/runtime', () => ({
    acquireRendition: () => ({ promise: new Promise(() => {}), release: vi.fn() }),
    subscribeRenditionReset: () => vi.fn(),
}));
vi.mock('../gallery-policy', () => ({
    acquireOriginalViewerBudget: () => vi.fn(),
    subscribeGalleryPolicy: () => vi.fn(),
}));
vi.mock('../../ui/gallery/gallery-controller', () => ({ setActive: vi.fn() }));
vi.mock('../encryption', () => ({ loadEncryptionStatus: vi.fn() }));
vi.mock('../transfers', () => ({ enqueueDownload: vi.fn() }));
vi.mock('./preview-info', () => ({ renderImageInfoHTML: vi.fn(() => '') }));

const PREVIEW_MARKUP = `
    <div id="preview-shell" role="dialog">
        <div id="preview-filename"></div>
        <button id="preview-info-btn" type="button"></button>
        <button id="preview-download" type="button"></button>
        <button id="preview-close" type="button"></button>
        <button id="preview-prev" type="button"></button>
        <button id="preview-next" type="button"></button>
        <div id="preview-counter"></div>
        <div id="preview-stage">
            <div id="preview-loading"><div id="preview-loading-fill"></div></div>
            <div id="preview-error"></div>
            <img id="preview-thumbnail" alt="">
            <img id="preview-image" alt="Preview">
        </div>
        <aside id="preview-info"><button id="preview-info-close" type="button"></button><div id="preview-info-body"></div></aside>
        <div id="preview-locked">
            <input id="preview-locked-input" type="password">
            <button id="preview-locked-eye" type="button"></button>
            <button id="preview-locked-unlock" type="button"></button>
            <div id="preview-locked-error"></div>
            <div id="preview-locked-hint"><span id="preview-locked-hint-text"></span></div>
        </div>
    </div>
`;

vi.mock('../../ui/mount', () => ({
    mountSvelte: vi.fn((_component: unknown, { target }: { target: HTMLElement }) => {
        target.innerHTML = PREVIEW_MARKUP;
        return { destroy: vi.fn(async () => undefined) };
    }),
}));
vi.mock('../../ui/preview/PreviewModal.svelte', () => ({ default: {} }));

// The module registry is reset between tests, so the preview and the test have
// to be handed the same instance of the shared state rather than each holding
// their own copy of it.
let state: typeof import('../../state').state;

/** Puts one file where the preview looks for it: the selection. */
function select(name: string): void {
    const item: FileCommandItem = { type: 'file', id: 42, name, size: 1024, parentId: '', source: 'fs' };
    state.selectedItems = new Map([['file:42', item]]);
}

/** The press under test, from outside any list that would claim it first. */
function pressSpace(): void {
    window.dispatchEvent(new KeyboardEvent('keydown', { key: ' ', code: 'Space', bubbles: true, cancelable: true }));
}

async function bootPreview() {
    state = (await import('../../state')).state;
    state.selectedItems = new Map();
    state.virtualView = null;
    const preview = await import('./preview');
    preview.activatePreviewModal();
    return preview;
}

beforeEach(() => {
    vi.resetModules();
    vi.clearAllMocks();
    document.body.innerHTML = `<div id="preview-modal" class="modal-overlay" style="display:none" aria-hidden="true">${PREVIEW_MARKUP}</div>`;
});

afterEach(async () => {
    (await import('./preview')).teardownPreviewModal();
});

describe('space on a file with no preview', () => {
    it('plays a video instead of complaining that it is not an image', async () => {
        await bootPreview();
        select('holiday.mov');
        pressSpace();

        expect(actions.playVideo).toHaveBeenCalledWith(expect.objectContaining({ id: 42, name: 'holiday.mov' }));
        expect(notifications.notify).not.toHaveBeenCalled();
    });

    it('opens a PDF in the viewer', async () => {
        await bootPreview();
        select('lease.pdf');
        pressSpace();

        expect(actions.openFile).toHaveBeenCalledWith(expect.objectContaining({ id: 42, name: 'lease.pdf' }));
        expect(actions.playVideo).not.toHaveBeenCalled();
    });

    it('still says so for a file nothing can open', async () => {
        await bootPreview();
        select('archive.zip');
        pressSpace();

        expect(actions.openFile).not.toHaveBeenCalled();
        expect(actions.playVideo).not.toHaveBeenCalled();
        expect(notifications.notify).toHaveBeenCalledOnce();
    });

    it('opens nothing from the trash, where a row is only a record', async () => {
        await bootPreview();
        state.virtualView = 'trash';
        select('holiday.mov');
        pressSpace();

        expect(actions.playVideo).not.toHaveBeenCalled();
    });
});

// Keyboard actions must respect controls and other modal owners while keeping
// desktop browsing available immediately after focus moves to the close button.
describe('preview keyboard ownership', () => {
    it('opens the selected photo, switches selections and toggles the same photo closed', async () => {
        await bootPreview();
        select('First.jpg');
        pressSpace();
        await vi.waitFor(() => expect(document.getElementById('preview-image')!.getAttribute('src')).toContain('image-42'));
        expect(document.getElementById('preview-filename')!.textContent).toBe('First.jpg');
        state.selectedItems = new Map([['file:43', { type: 'file', id: 43, name: 'Second.jpg', size: 512, parentId: '', source: 'fs' }]]);
        pressSpace();
        await vi.waitFor(() => expect(document.getElementById('preview-image')!.getAttribute('src')).toContain('image-43'));
        pressSpace();
        expect(document.getElementById('preview-modal')!.style.display).toBe('none');
    });

    it('hands a newly selected video off and closes the photo', async () => {
        const preview = await bootPreview();
        select('Photo.jpg');
        await preview.openPreviewForSelection();
        select('Movie.mp4');
        pressSpace();
        expect(actions.playVideo).toHaveBeenCalledWith(expect.objectContaining({ name: 'Movie.mp4' }));
        expect(document.getElementById('preview-modal')!.style.display).toBe('none');
    });

    it('keeps the photo when a new selection cannot be previewed', async () => {
        const preview = await bootPreview();
        select('Photo.jpg');
        await preview.openPreviewForSelection();
        select('archive.zip');
        pressSpace();
        expect(document.getElementById('preview-modal')!.style.display).toBe('flex');
        expect(notifications.notify).toHaveBeenCalledWith(expect.objectContaining({ title: 'TDrive cannot open this kind of file' }));
    });

    it('reports multiple selection and does nothing for an empty selection', async () => {
        const preview = await bootPreview();
        expect(await preview.openPreviewForSelection()).toBe(false);
        pressSpace();
        expect(notifications.notify).not.toHaveBeenCalled();
        select('Photo.jpg');
        state.selectedItems.set('file:43', { type: 'file', id: 43, name: 'Second.jpg', size: 512, parentId: '', source: 'fs' });
        expect(await preview.openPreviewForSelection()).toBe(false);
        expect(notifications.notify).toHaveBeenCalledWith(expect.objectContaining({ title: 'Preview works with one image at a time' }));
        expect(document.getElementById('preview-modal')!.style.display).toBe('none');
    });

    it.each(['INPUT', 'TEXTAREA', 'SELECT'])('leaves Space with a focused %s', async tag => {
        await bootPreview();
        select('Photo.jpg');
        const control = document.createElement(tag);
        document.body.append(control);
        control.focus();
        pressSpace();
        expect(document.getElementById('preview-modal')!.style.display).toBe('none');
        expect(notifications.notify).not.toHaveBeenCalled();
    });

    it.each(['metaKey', 'ctrlKey', 'altKey'])('leaves modified Space shortcuts alone (%s)', async modifier => {
        await bootPreview();
        select('Photo.jpg');
        const key = new KeyboardEvent('keydown', { key: ' ', code: 'Space', [modifier]: true, cancelable: true });
        window.dispatchEvent(key);
        expect(key.defaultPrevented).toBe(false);
        expect(document.getElementById('preview-modal')!.style.display).toBe('none');
    });

    it('leaves claimed Space and Space under another overlay or context menu alone', async () => {
        await bootPreview();
        select('Photo.jpg');
        const claimed = new KeyboardEvent('keydown', { key: ' ', cancelable: true });
        claimed.preventDefault();
        window.dispatchEvent(claimed);
        expect(document.getElementById('preview-modal')!.style.display).toBe('none');
        const overlay = document.createElement('div');
        overlay.className = 'modal-overlay';
        overlay.style.display = 'flex';
        document.body.append(overlay);
        pressSpace();
        expect(document.getElementById('preview-modal')!.style.display).toBe('none');
        overlay.remove();
        document.body.insertAdjacentHTML('beforeend', '<div id="context-menu"><div class="context-menu-panel"></div></div>');
        pressSpace();
        expect(document.getElementById('preview-modal')!.style.display).toBe('none');
    });

    it('navigates from buttons but yields arrows and info shortcuts to text controls and modifiers', async () => {
        const preview = await bootPreview();
        const first = { type: 'file' as const, id: 1, name: 'First.jpg' };
        await preview.openPreviewList([first, { ...first, id: 2, name: 'Second.jpg' }], 0);
        const input = document.getElementById('preview-locked-input')!;
        input.focus();
        window.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight' }));
        window.dispatchEvent(new KeyboardEvent('keydown', { key: 'i' }));
        expect(document.getElementById('preview-counter')!.textContent).toBe('1 / 2');
        expect(document.getElementById('preview-modal')!.classList.contains('is-info-open')).toBe(false);
        document.getElementById('preview-close')!.focus();
        window.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', altKey: true }));
        window.dispatchEvent(new KeyboardEvent('keydown', { key: 'i', ctrlKey: true }));
        expect(document.getElementById('preview-counter')!.textContent).toBe('1 / 2');
        expect(document.getElementById('preview-modal')!.classList.contains('is-info-open')).toBe(false);
        window.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight' }));
        await vi.waitFor(() => expect(document.getElementById('preview-counter')!.textContent).toBe('2 / 2'));
        window.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft' }));
        await vi.waitFor(() => expect(document.getElementById('preview-counter')!.textContent).toBe('1 / 2'));
        window.dispatchEvent(new KeyboardEvent('keydown', { key: 'I' }));
        expect(document.getElementById('preview-modal')!.classList.contains('is-info-open')).toBe(true);
        window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', cancelable: true }));
        expect(document.getElementById('preview-modal')!.style.display).toBe('none');
    });

    it('does not reinterpret arrows for a standalone photo', async () => {
        const preview = await bootPreview();
        select('Photo.jpg');
        await preview.openPreviewForSelection();
        const event = new KeyboardEvent('keydown', { key: 'ArrowRight', cancelable: true });
        window.dispatchEvent(event);
        expect(event.defaultPrevented).toBe(false);
        expect(document.getElementById('preview-filename')!.textContent).toBe('Photo.jpg');
    });
});
