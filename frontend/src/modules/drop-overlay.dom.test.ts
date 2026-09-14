import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { state } from '../state';
import { activateDropOverlay } from './drop-overlay';

let disposeDropOverlay: (() => void) | undefined;

function fileDrag(type: string, kinds: string[]) {
    const event = new Event(type, { bubbles: true, cancelable: true });
    Object.defineProperty(event, 'dataTransfer', { value: { types: kinds } });
    return event;
}

describe('drop overlay', () => {
    beforeEach(() => {
        vi.useFakeTimers();
        document.body.innerHTML = `
            <div id="file-list"></div>
            <div id="drop-overlay" hidden><strong id="drop-overlay-title"></strong></div>`;
        Object.defineProperty(document.getElementById('file-list'), 'getBoundingClientRect', {
            value: () => ({ top: 120, left: 240, width: 800, height: 500 }),
            configurable: true,
        });
        state.activeChannel = { id: 1, title: 'Personal', kind: 'personal' };
        state.folderPath = [{ id: 'a', name: 'Photos' }];
        state.dragState = null;
        disposeDropOverlay = activateDropOverlay();
    });

    afterEach(() => {
        disposeDropOverlay?.();
        disposeDropOverlay = undefined;
        vi.useRealTimers();
        state.activeChannel = null;
        state.folderPath = [];
    });

    it('covers the file list with the target folder name while an OS file drag hovers, then fades out', async () => {
        const overlay = document.getElementById('drop-overlay')!;

        window.dispatchEvent(fileDrag('dragover', ['Files']));
        await vi.advanceTimersByTimeAsync(20);
        expect(overlay.hidden).toBe(false);
        expect(overlay.style.top).toBe('120px');
        expect(overlay.style.width).toBe('800px');
        expect(document.getElementById('drop-overlay-title')?.textContent).toBe('Drop to add to Photos');

        vi.advanceTimersByTime(100);
        window.dispatchEvent(fileDrag('dragover', ['Files']));
        vi.advanceTimersByTime(100);
        expect(overlay.hidden).toBe(false);

        vi.advanceTimersByTime(400);
        expect(overlay.hidden).toBe(true);
    });

    it('coalesces a dragover burst into one layout read per frame', async () => {
        const list = document.getElementById('file-list')!;
        const rectSpy = vi.spyOn(list, 'getBoundingClientRect');

        for (let index = 0; index < 12; index += 1) {
            window.dispatchEvent(fileDrag('dragover', ['Files']));
        }
        expect(rectSpy).not.toHaveBeenCalled();

        await vi.advanceTimersByTimeAsync(20);
        expect(rectSpy).toHaveBeenCalledTimes(1);
    });

    it('ignores in-app row drags and hides immediately on drop', () => {
        const overlay = document.getElementById('drop-overlay')!;

        window.dispatchEvent(fileDrag('dragover', ['text/plain']));
        expect(overlay.hidden).toBe(true);

        window.dispatchEvent(fileDrag('dragover', ['Files']));
        expect(overlay.hidden).toBe(false);
        window.dispatchEvent(fileDrag('drop', ['Files']));
        vi.advanceTimersByTime(200);
        expect(overlay.hidden).toBe(true);
    });

    it('names the drive at the root and stays hidden without an open drive', () => {
        state.folderPath = [];
        window.dispatchEvent(fileDrag('dragover', ['Files']));
        expect(document.getElementById('drop-overlay-title')?.textContent).toBe('Drop to add to Personal');
        window.dispatchEvent(fileDrag('drop', ['Files']));
        vi.advanceTimersByTime(200);

        state.activeChannel = null;
        window.dispatchEvent(fileDrag('dragover', ['Files']));
        expect(document.getElementById('drop-overlay')?.hidden).toBe(true);
    });
});
