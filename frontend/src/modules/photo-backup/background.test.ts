// @vitest-environment happy-dom
import { writable } from 'svelte/store';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { PhotoBackupState } from '../../api/photo-backup';

const mocks = vi.hoisted(() => ({
    lease: vi.fn(), demand: vi.fn(), ios: vi.fn(), android: true,
}));
vi.mock('../../api/photo-backup', () => ({ setPhotoBackupBackgroundLease: mocks.lease }));
vi.mock('../android-foreground', () => ({
    canRunInBackground: () => mocks.android,
    setPhotoBackupBackgroundDemand: mocks.demand,
}));
vi.mock('./native-adapter', () => ({ setIOSPhotoBackupBackground: mocks.ios }));
vi.mock('../../api/runtime', () => ({ runtimeEventsAvailable: () => false, onRuntimeEvent: vi.fn() }));

import { activatePhotoBackupBackground } from './background';

const state = (uploading: boolean): PhotoBackupState => ({
    settings: { enabled: true, photos: true, videos: true, wifiOnly: false, encrypt: false },
    sources: [], status: { phase: uploading ? 'uploading' : 'idle', pending: 0, uploading: uploading ? 1 : 0, complete: 0, failed: 0, paused: 0, bytesDone: 0, bytesTotal: 0, currentFile: '', currentFileBytesDone: 0, currentFileBytesTotal: 0, currentFilePercent: 0, message: '' },
    capabilities: { wifiOnly: { supported: true, label: '', detail: '' }, access: { status: '', detail: '' } },
    platform: 'android', destination: { id: '', title: '', kind: '' }, manualPaused: false, encryptionRequired: false,
});
const flush = async () => { await Promise.resolve(); await Promise.resolve(); await Promise.resolve(); };

describe('photo backup background lease', () => {
    beforeEach(() => {
        mocks.lease.mockReset().mockResolvedValue(undefined);
        mocks.demand.mockReset().mockResolvedValue(undefined);
        mocks.ios.mockReset().mockResolvedValue(true);
        mocks.android = true;
        Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
    });

    it('does not request mobile execution grants for desktop uploads', async () => {
        const report = vi.fn();
        const stop = activatePhotoBackupBackground(writable({ ...state(true), platform: 'darwin' }), report);
        await flush(); await flush();
        expect(mocks.demand).not.toHaveBeenCalled();
        expect(mocks.ios).not.toHaveBeenCalled();
        expect(report).not.toHaveBeenCalled();
        stop();
    });

    it('releases a delayed native grant when backup stops before acquisition completes', async () => {
        let grant!: () => void;
        mocks.demand.mockImplementationOnce(() => new Promise<void>((resolve) => { grant = resolve; })).mockResolvedValue(undefined);
        const store = writable<PhotoBackupState | null>(state(true));
        const stop = activatePhotoBackupBackground(store, vi.fn());
        await flush(); store.set(state(false)); grant(); await flush(); await flush();
        expect(mocks.lease).not.toHaveBeenCalledWith(true);
        // No summary for a lease that never really started: nothing ran.
        expect(mocks.demand).toHaveBeenLastCalledWith(null, undefined);
        stop();
    });

    // A backup that ran while the phone was in a pocket leaves one line saying
    // so: the ongoing notification disappears with the service, and its absence
    // tells the user nothing about whether their photos are safe. It counts
    // this run, not every backup the drive has ever done.
    it('leaves a summary behind counting what this run added', async () => {
        const store = writable<PhotoBackupState | null>(state(true));
        const stop = activatePhotoBackupBackground(store, vi.fn());
        await flush();

        const done = state(false);
        done.status.complete = 3;
        store.set(done);
        await flush(); await flush();

        expect(mocks.demand).toHaveBeenLastCalledWith(null, {
            outcome: 'complete', title: 'Photos backed up', text: '3 items added to your drive',
        });
        stop();
    });

    it('says so when a run left something behind', async () => {
        const store = writable<PhotoBackupState | null>(state(true));
        const stop = activatePhotoBackupBackground(store, vi.fn());
        await flush();

        const done = state(false);
        done.status.complete = 2;
        done.status.failed = 1;
        store.set(done);
        await flush(); await flush();

        expect(mocks.demand).toHaveBeenLastCalledWith(null, {
            outcome: 'stopped', title: 'Photo backup needs attention', text: '1 item could not be backed up',
        });
        stop();
    });

    it('does not arm Go and reports a failed native acquisition', async () => {
        mocks.demand.mockRejectedValueOnce(new Error('denied'));
        const report = vi.fn();
        const stop = activatePhotoBackupBackground(writable(state(true)), report);
        await flush();
        expect(mocks.lease).not.toHaveBeenCalledWith(true);
        expect(report).toHaveBeenCalledTimes(1);
        stop();
    });
});
