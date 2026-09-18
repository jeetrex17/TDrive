// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const bridge = vi.hoisted(() => ({ callBridge: vi.fn(), hasBridgeMethod: vi.fn() }));
vi.mock('./android-bridge', () => bridge);

import type { ForegroundNotice } from './android-foreground';

type Foreground = typeof import('./android-foreground');
type Payload = Record<string, unknown>;

let foreground: Foreground;

function notice(over: Partial<ForegroundNotice> = {}): ForegroundNotice {
    return {
        title: 'Uploading 3 files',
        text: '679 MB of 1.1 GB · 2 min left',
        progress: 50,
        ...over,
    };
}

/** Every payload the host has been handed, in order. */
function posted(): Payload[] {
    return bridge.callBridge.mock.calls.map((call) => JSON.parse((call[1] as string[])[0]) as Payload);
}

function setVisibility(state: 'visible' | 'hidden'): void {
    Object.defineProperty(document, 'visibilityState', { value: state, configurable: true });
}

beforeEach(async () => {
    vi.useFakeTimers();
    bridge.callBridge.mockReset();
    bridge.callBridge.mockResolvedValue('');
    bridge.hasBridgeMethod.mockReturnValue(true);
    setVisibility('hidden');
    // The module holds what the host was last told, which is exactly the state
    // under test: every case starts from a page that has said nothing yet.
    vi.resetModules();
    foreground = await import('./android-foreground');
});

afterEach(() => {
    vi.useRealTimers();
});

describe('android foreground notice', () => {
    it('hands the host everything the notification can show', async () => {
        await foreground.runInBackground(notice({ detail: 'IMG_0042.HEIC', filesDone: 3, filesTotal: 12 }));
        expect(posted()).toEqual([{
            running: true,
            title: 'Uploading 3 files',
            text: '679 MB of 1.1 GB · 2 min left',
            progress: 50,
            detail: 'IMG_0042.HEIC',
            filesDone: 3,
            filesTotal: 12,
        }]);
    });

    it('leaves out what it does not know rather than inventing a count', async () => {
        await foreground.runInBackground(notice({ progress: -1 }));
        const [first] = posted();
        expect(first).not.toHaveProperty('filesTotal');
        expect(first).not.toHaveProperty('detail');
        expect(first.progress).toBe(-1);
    });

    it('starts at once, then posts at most once a second with the latest figures', async () => {
        await foreground.runInBackground(notice({ progress: 10 }));
        await foreground.runInBackground(notice({ progress: 20 }));
        await foreground.runInBackground(notice({ progress: 30 }));
        expect(posted()).toHaveLength(1);

        await vi.advanceTimersByTimeAsync(1000);
        expect(posted()).toHaveLength(2);
        expect(posted()[1].progress).toBe(30);
    });

    it('stops without waiting for the floor, and says how the work ended', async () => {
        await foreground.runInBackground(notice());
        await foreground.stopRunningInBackground({
            outcome: 'complete',
            title: 'Upload complete',
            text: '12 files backed up',
        });
        expect(posted()).toEqual([
            expect.objectContaining({ running: true }),
            { running: false, outcome: 'complete', title: 'Upload complete', text: '12 files backed up' },
        ]);
    });

    it('keeps quiet about the ending while the user is looking at the app', async () => {
        setVisibility('visible');
        await foreground.runInBackground(notice());
        await foreground.stopRunningInBackground({ outcome: 'complete', title: 'Done', text: '12 files' });
        expect(posted()[1]).toEqual({ running: false });
    });

    it('never claims work is over while the other side of the app is still running', async () => {
        await foreground.setPhotoBackupBackgroundDemand(notice({ title: 'Backing up photos' }));
        await foreground.runInBackground(notice());
        await foreground.setPhotoBackupBackgroundDemand(null, {
            outcome: 'complete',
            title: 'Backup complete',
            text: '40 photos',
        });
        await vi.advanceTimersByTimeAsync(1000);

        expect(posted().every((payload) => payload.running === true)).toBe(true);
        expect(posted()[posted().length - 1]).toMatchObject({ title: 'Uploading 3 files' });
    });

    it('is inert where the host has no such service', () => {
        bridge.hasBridgeMethod.mockReturnValue(false);
        expect(foreground.canRunInBackground()).toBe(false);
    });
});
