import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
    markTransferDone: vi.fn(),
    pushTransferStart: vi.fn(),
    updateTransferProgress: vi.fn(),
}));

const listeners = vi.hoisted(() => new Map<string, (...args: unknown[]) => void>());
const eventsOn = vi.hoisted(() => vi.fn((name: string, callback: (event: { name: string; data: unknown[] }) => void) => {
    listeners.set(name, (...args: unknown[]) => callback({ name, data: args }));
    return () => listeners.delete(name);
}));

vi.mock('@wailsio/runtime', () => ({ Events: { On: eventsOn } }));
vi.mock('./notif-bell', () => ({
    markTransferDone: mocks.markTransferDone,
    pushTransferStart: mocks.pushTransferStart,
    updateTransferProgress: mocks.updateTransferProgress,
}));

import { activateFileSelectionProgress, selectionCopyProgress } from './file-selection';

/** Delivers a host tick the way Go does: the payload as the event's single arg. */
function tick(payload: unknown): void {
    listeners.get('common:filepicker')?.(payload);
}

let stop = () => {};

beforeEach(() => {
    vi.clearAllMocks();
    listeners.clear();
    vi.useFakeTimers();
    stop = activateFileSelectionProgress();
});

afterEach(() => {
    stop();
    vi.useRealTimers();
});

describe('selectionCopyProgress', () => {
    it('reads a host tick', () => {
        expect(selectionCopyProgress({ phase: 'copying', done: 2, total: 7 }))
            .toEqual({ done: 2, total: 7, finished: false });
    });

    it('treats the final tick as finished however it is spelled', () => {
        expect(selectionCopyProgress({ phase: 'done', done: 7, total: 7 })?.finished).toBe(true);
        // A host that omits the phase but has counted everything is still done.
        expect(selectionCopyProgress({ done: 7, total: 7 })?.finished).toBe(true);
    });

    it('refuses anything it cannot act on', () => {
        for (const payload of [null, undefined, 'copying', 42, {}, { done: 1 }, { total: 0, done: 0 }, { total: 3, done: -1 }]) {
            expect(selectionCopyProgress(payload)).toBeNull();
        }
    });

    it('never lets a miscounting host drive the row past its end', () => {
        expect(selectionCopyProgress({ done: 9, total: 4 })).toEqual({ done: 4, total: 4, finished: true });
    });
});

describe('activateFileSelectionProgress', () => {
    it('stays silent for a copy that finishes inside the picker dismissal', () => {
        tick({ phase: 'copying', done: 0, total: 2 });
        vi.advanceTimersByTime(100);
        tick({ phase: 'done', done: 2, total: 2 });
        vi.advanceTimersByTime(1000);

        // A row that appears and vanishes in one breath reads as a glitch.
        expect(mocks.pushTransferStart).not.toHaveBeenCalled();
        expect(mocks.markTransferDone).not.toHaveBeenCalled();
    });

    it('shows one row for a copy long enough to need explaining, then closes it', () => {
        tick({ phase: 'copying', done: 0, total: 7 });
        vi.advanceTimersByTime(300);

        expect(mocks.pushTransferStart).toHaveBeenCalledTimes(1);
        // total is bytes, not files -- the count travels as itemsTotal.
        expect(mocks.pushTransferStart).toHaveBeenCalledWith(
            expect.objectContaining({ name: 'Preparing 7 files…', total: 0, direction: 'up' }),
        );

        tick({ phase: 'copying', done: 3, total: 7 });
        expect(mocks.updateTransferProgress).toHaveBeenLastCalledWith(
            expect.objectContaining({ progress: 43, itemsDone: 3, itemsTotal: 7 }),
        );

        tick({ phase: 'done', done: 7, total: 7 });
        // The upload that follows owns the screen from here; two rows for the
        // same files would only disagree with each other.
        expect(mocks.markTransferDone).toHaveBeenCalledTimes(1);
        expect(mocks.pushTransferStart).toHaveBeenCalledTimes(1);
    });

    it('counts one file in the singular', () => {
        tick({ phase: 'copying', done: 0, total: 1 });
        vi.advanceTimersByTime(300);
        expect(mocks.pushTransferStart).toHaveBeenCalledWith(
            expect.objectContaining({ name: 'Preparing 1 file…' }),
        );
    });

    it('does not leave a row behind when it is torn down mid-copy', () => {
        tick({ phase: 'copying', done: 1, total: 9 });
        vi.advanceTimersByTime(300);
        expect(mocks.pushTransferStart).toHaveBeenCalledTimes(1);

        stop();
        expect(mocks.markTransferDone).toHaveBeenCalledTimes(1);

        // And the teardown really unsubscribed: a late tick cannot revive it.
        tick({ phase: 'copying', done: 2, total: 9 });
        vi.advanceTimersByTime(300);
        expect(mocks.pushTransferStart).toHaveBeenCalledTimes(1);
    });

    it('opens a second selection cleanly after the first finished', () => {
        tick({ phase: 'copying', done: 0, total: 3 });
        vi.advanceTimersByTime(300);
        tick({ phase: 'done', done: 3, total: 3 });

        tick({ phase: 'copying', done: 0, total: 5 });
        vi.advanceTimersByTime(300);
        expect(mocks.pushTransferStart).toHaveBeenCalledTimes(2);
        expect(mocks.pushTransferStart).toHaveBeenLastCalledWith(
            expect.objectContaining({ name: 'Preparing 5 files…', total: 0 }),
        );
    });
});
