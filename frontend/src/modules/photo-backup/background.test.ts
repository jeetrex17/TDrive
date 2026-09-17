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
    settings: { enabled: true, photos: true, videos: true, futureOnly: false, wifiOnly: false, encrypt: false },
    sources: [], status: { phase: uploading ? 'uploading' : 'idle', pending: 0, uploading: uploading ? 1 : 0, complete: 0, failed: 0, paused: 0, bytesDone: 0, bytesTotal: 0, message: '' },
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
        expect(mocks.demand).toHaveBeenLastCalledWith(null);
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
