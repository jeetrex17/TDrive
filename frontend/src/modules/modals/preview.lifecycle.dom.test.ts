import { beforeEach, describe, expect, it, vi } from 'vitest';

const runtime = vi.hoisted(() => ({
    onRuntimeEvent: vi.fn((eventName: string, callback: (...args: unknown[]) => void) => {
        runtime.callbacks.set(eventName, callback);
        return vi.fn();
    }),
    callbacks: new Map<string, (...args: unknown[]) => void>(),
}));

vi.mock('../../api', () => ({
    getPreviewFile: vi.fn(),
    getPreviewThumbnail: vi.fn(),
    hasOperationErrorCode: vi.fn(),
    isMobilePlatform: vi.fn(() => false),
    onRuntimeEvent: runtime.onRuntimeEvent,
    openExternalUrl: vi.fn(),
    useEncryptionPassword: vi.fn(),
}));
vi.mock('../notifications', () => ({ notify: vi.fn() }));
vi.mock('../encryption', () => ({ loadEncryptionStatus: vi.fn() }));
vi.mock('../transfers', () => ({ enqueueDownload: vi.fn() }));
vi.mock('./preview-info', () => ({ renderImageInfoHTML: vi.fn(() => '') }));
vi.mock('./preview-transition', () => ({
    capturePreviewTransitionSource: vi.fn(() => null),
    createPreviewTransitionController: vi.fn(() => ({
        cancel: vi.fn(),
        isRunning: vi.fn(() => false),
        finishOpen: vi.fn(() => false),
        beginOpen: vi.fn(() => false),
        playClose: vi.fn(),
    })),
}));
vi.mock('../../ui/preview/PreviewModal.svelte', () => ({ default: {} }));

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

beforeEach(() => {
    vi.resetModules();
    vi.clearAllMocks();
    runtime.callbacks.clear();
    document.body.innerHTML = `<div id="preview-modal" class="modal-overlay" style="display:none" aria-hidden="true">${PREVIEW_MARKUP}</div>`;
});

describe('preview modal lifecycle', () => {
    it('binds setup listeners and the progress subscription once per host', async () => {
        const preview = await import('./preview');

        preview.activatePreviewModal();
        preview.activatePreviewModal();

        expect(runtime.onRuntimeEvent).toHaveBeenCalledTimes(1);
    });

    it('tears down the old host before binding a replacement host', async () => {
        const preview = await import('./preview');
        preview.activatePreviewModal();
        const oldHost = document.getElementById('preview-modal')!;
        const replacement = document.createElement('div');
        replacement.id = 'preview-modal';
        replacement.className = 'modal-overlay';
        replacement.style.display = 'none';
        replacement.setAttribute('aria-hidden', 'true');
        replacement.innerHTML = PREVIEW_MARKUP;
                oldHost.replaceWith(replacement);

        preview.activatePreviewModal();
        expect(runtime.onRuntimeEvent).toHaveBeenCalledTimes(2);
    });
});
