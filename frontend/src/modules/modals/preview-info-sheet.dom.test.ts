// The lightbox info card is a bottom sheet on a phone, so it has to be
// dismissible the way a sheet is: a tap on the picture behind it, a drag down,
// and Android's BACK press, which must reach the sheet before the preview.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api', () => ({
    getPreviewFile: vi.fn(),
    getPreviewThumbnail: vi.fn(),
    hasOperationErrorCode: vi.fn(),
    isMobilePlatform: vi.fn(() => true),
    onRuntimeEvent: vi.fn(() => vi.fn()),
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

function pointer(el: Element, type: string, x: number, y: number): void {
    el.dispatchEvent(new PointerEvent(type, {
        pointerId: 1,
        pointerType: 'touch',
        isPrimary: true,
        clientX: x,
        clientY: y,
        bubbles: true,
        cancelable: true,
    }));
}

// resetModules gives each test its own module instances, so the back stack has
// to be pulled in here rather than at the top of the file.
async function openInfo() {
    const preview = await import('./preview');
    const backStack = await import('../../ui/modals/sheet-stack');
    preview.activatePreviewModal();
    document.getElementById('preview-info-btn')!.click();
    expect(infoOpen()).toBe(true);
    return backStack;
}

function infoOpen(): boolean {
    return document.getElementById('preview-modal')!.classList.contains('is-info-open');
}

beforeEach(() => {
    vi.useFakeTimers();
    vi.resetModules();
    vi.clearAllMocks();
    document.body.innerHTML = `<div id="preview-modal" class="modal-overlay" style="display:flex">${PREVIEW_MARKUP}</div>`;
});

afterEach(() => {
    vi.useRealTimers();
});

describe('the phone info sheet', () => {
    it('closes on a tap on the picture instead of toggling the chrome', async () => {
        await openInfo();
        const modal = document.getElementById('preview-modal')!;
        modal.classList.remove('is-chrome-visible');
        const stage = document.getElementById('preview-stage')!;

        pointer(stage, 'pointerdown', 120, 300);
        pointer(stage, 'pointerup', 120, 300);
        // The stage waits out the double-tap window before reporting a tap.
        vi.advanceTimersByTime(300);

        expect(infoOpen()).toBe(false);
        expect(modal.classList.contains('is-chrome-visible')).toBe(false);
        expect(modal.style.display).toBe('flex');
    });

    it('closes on a drag down and springs back on a short one', async () => {
        await openInfo();
        const sheet = document.getElementById('preview-info')!;

        pointer(sheet, 'pointerdown', 120, 400);
        pointer(sheet, 'pointermove', 120, 420);
        pointer(sheet, 'pointerup', 120, 420);
        expect(infoOpen()).toBe(true);
        expect(sheet.style.transform).toBe('');

        pointer(sheet, 'pointerdown', 120, 400);
        pointer(sheet, 'pointermove', 120, 450);
        pointer(sheet, 'pointermove', 120, 540);
        pointer(sheet, 'pointerup', 120, 540);
        expect(infoOpen()).toBe(false);
        expect(sheet.style.transform).toBe('translateY(100%)');
    });

    it('takes the BACK press before the preview does, and gives it back after', async () => {
        const backStack = await openInfo();
        expect(backStack.hasOpenSheet()).toBe(true);

        expect(backStack.closeTopSheet()).toBe(true);
        expect(infoOpen()).toBe(false);
        expect(document.getElementById('preview-modal')!.style.display).toBe('flex');
        expect(backStack.hasOpenSheet()).toBe(false);
    });

    it('does not leave the sheet on the back stack when the close button is used', async () => {
        const backStack = await openInfo();
        document.getElementById('preview-info-close')!.click();

        expect(infoOpen()).toBe(false);
        expect(backStack.hasOpenSheet()).toBe(false);
    });
});
