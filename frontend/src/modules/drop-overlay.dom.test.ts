import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import { state } from '../state';
import DropOverlay from '../ui/transfers/DropOverlay.svelte';
import { dropOverlayState, resetDropOverlayState } from '../ui/transfers/drop-overlay-store';
import { activateDropOverlay } from './drop-overlay';

let disposeDropOverlay: (() => void) | undefined;
let overlayComponent: Record<string, unknown> | null = null;

function fileDrag(type: string, kinds: string[]) {
    const event = new Event(type, { bubbles: true, cancelable: true });
    Object.defineProperty(event, 'dataTransfer', { value: { types: kinds } });
    return event;
}

// The controller publishes to a store and Svelte renders from it on a
// microtask, so every assertion about the element reads it after a flush.
function overlay(): HTMLElement {
    flushSync();
    return document.querySelector<HTMLElement>('.drop-overlay')!;
}

function title(): string {
    flushSync();
    return document.querySelector('.drop-overlay-title')?.textContent ?? '';
}

describe('drop overlay', () => {
    beforeEach(() => {
        vi.useFakeTimers();
        document.body.innerHTML = '<div id="file-list"></div><div id="overlay-host"></div>';
        Object.defineProperty(document.getElementById('file-list'), 'getBoundingClientRect', {
            value: () => ({ top: 120, left: 240, width: 800, height: 500 }),
            configurable: true,
        });
        overlayComponent = mount(DropOverlay, {
            target: document.getElementById('overlay-host')!,
            intro: false,
        });
        state.activeChannel = { id: 1, title: 'Personal', kind: 'personal' };
        state.folderPath = [{ id: 'a', name: 'Photos' }];
        state.dragState = null;
        disposeDropOverlay = activateDropOverlay();
    });

    afterEach(() => {
        disposeDropOverlay?.();
        disposeDropOverlay = undefined;
        if (overlayComponent) void unmount(overlayComponent, { outro: false });
        overlayComponent = null;
        resetDropOverlayState();
        vi.useRealTimers();
        state.activeChannel = null;
        state.folderPath = [];
    });

    it('covers the file list with the target folder name while an OS file drag hovers, then fades out', async () => {
        window.dispatchEvent(fileDrag('dragover', ['Files']));
        await vi.advanceTimersByTimeAsync(20);
        expect(overlay().hidden).toBe(false);
        expect(overlay().style.top).toBe('120px');
        expect(overlay().style.width).toBe('800px');
        expect(overlay().classList.contains('is-visible')).toBe(true);
        expect(title()).toBe('Drop to add to Photos');

        vi.advanceTimersByTime(100);
        window.dispatchEvent(fileDrag('dragover', ['Files']));
        vi.advanceTimersByTime(100);
        expect(overlay().hidden).toBe(false);

        vi.advanceTimersByTime(400);
        expect(overlay().hidden).toBe(true);
    });

    it('keeps the card in the layout while it fades, and only then takes it away', async () => {
        window.dispatchEvent(fileDrag('dragover', ['Files']));
        await vi.advanceTimersByTimeAsync(20);

        window.dispatchEvent(fileDrag('drop', ['Files']));
        expect(overlay().hidden).toBe(false);
        expect(overlay().classList.contains('is-visible')).toBe(false);

        vi.advanceTimersByTime(200);
        expect(overlay().hidden).toBe(true);
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

    it('publishes nothing while a steady drag keeps firing dragover', async () => {
        window.dispatchEvent(fileDrag('dragover', ['Files']));
        await vi.advanceTimersByTimeAsync(20);

        // A drag across the window fires hundreds of dragovers and nothing
        // about the overlay changes between one and the next, so the controller
        // must go quiet rather than allocate a state object per pointer move.
        let updates = 0;
        const stop = dropOverlayState.subscribe(() => { updates += 1; });
        expect(updates).toBe(1); // the subscription's own initial call

        for (let index = 0; index < 20; index += 1) {
            window.dispatchEvent(fileDrag('dragover', ['Files']));
        }
        expect(updates).toBe(1);
        stop();
    });

    it('ignores in-app row drags and hides immediately on drop', () => {
        window.dispatchEvent(fileDrag('dragover', ['text/plain']));
        expect(overlay().hidden).toBe(true);

        window.dispatchEvent(fileDrag('dragover', ['Files']));
        expect(overlay().hidden).toBe(false);
        window.dispatchEvent(fileDrag('drop', ['Files']));
        vi.advanceTimersByTime(200);
        expect(overlay().hidden).toBe(true);
    });

    it('names the drive at the root and stays hidden without an open drive', () => {
        state.folderPath = [];
        window.dispatchEvent(fileDrag('dragover', ['Files']));
        expect(title()).toBe('Drop to add to Personal');
        window.dispatchEvent(fileDrag('drop', ['Files']));
        vi.advanceTimersByTime(200);

        state.activeChannel = null;
        window.dispatchEvent(fileDrag('dragover', ['Files']));
        expect(overlay().hidden).toBe(true);
    });
});
