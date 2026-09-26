// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest';
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
    openOriginalImage: vi.fn(),
}));
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
