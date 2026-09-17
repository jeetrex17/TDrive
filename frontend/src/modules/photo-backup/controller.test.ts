// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';
import type { PhotoBackupState } from '../../api/photo-backup';

const mocks = vi.hoisted(() => ({
    events: new Map<string, (payload: unknown) => void>(),
    state: null as PhotoBackupState | null,
    enqueue: vi.fn(), run: vi.fn(), pause: vi.fn(), resume: vi.fn(), retry: vi.fn(), policy: vi.fn(),
    list: vi.fn(), materialize: vi.fn(), release: vi.fn(),
}));

vi.mock('../../api/photo-backup', () => ({
    defaultSettings: { enabled: false, photos: true, videos: true, futureOnly: false, wifiOnly: false, chargingOnly: false },
    getPhotoBackupState: vi.fn(() => Promise.resolve(mocks.state)),
    enqueuePhotoBackupAssets: mocks.enqueue,
    runPhotoBackup: mocks.run,
    pausePhotoBackup: mocks.pause,
    resumePhotoBackup: mocks.resume,
    setPhotoBackupPolicy: mocks.policy,
    savePhotoBackupSettings: vi.fn(), addPhotoBackupFolder: vi.fn(), removePhotoBackupSource: vi.fn(), retryPhotoBackup: mocks.retry, resolvePhotoBackupResource: vi.fn(), upsertPhotoBackupSource: vi.fn(),
}));
vi.mock('../../api/runtime', () => ({ runtimeEventsAvailable: () => true, onRuntimeEvent: (name: string, cb: (payload: unknown) => void) => { mocks.events.set(name, cb); return () => mocks.events.delete(name); } }));
vi.mock('./native-adapter', () => ({
    nativePhotoBackupAvailable: () => true,
    nativePhotoBackupPolicy: () => Promise.resolve({ wifi: true, charging: true }),
    requestNativePhotoBackupAccess: vi.fn(), listNativePhotoBackupSources: vi.fn(),
    listNativePhotoBackupAssets: mocks.list, materializeNativePhotoBackupAsset: mocks.materialize, releaseNativePhotoBackupAsset: mocks.release,
}));

import { activatePhotoBackup, pausePhotoBackupNow, photoBackupError, resumePhotoBackupNow, retryPhotoBackupNow, startPhotoBackup } from './controller';

const flush = async () => { for (let i = 0; i < 8; i += 1) await new Promise((resolve) => setTimeout(resolve, 0)); };
const state = (): PhotoBackupState => ({ settings: { enabled: true, photos: true, videos: true, futureOnly: false, wifiOnly: false, chargingOnly: false }, sources: [{ id: 'all', kind: 'library', root: 'library', name: 'All', enabled: true, addedAt: 0 }], status: { phase: 'idle', pending: 0, uploading: 0, complete: 0, failed: 0, paused: 0, bytesDone: 0, bytesTotal: 0, message: '' }, capabilities: { wifiOnly: { supported: true, label: '', detail: '' }, chargingOnly: { supported: true, label: '', detail: '' }, access: { status: 'granted', detail: '' } }, platform: 'android', destination: { id: '1', title: 'Personal', kind: 'personal' }, manualPaused: false });

describe('photo backup controller scheduler', () => {
    let stop = () => {};
    beforeEach(() => { stop(); mocks.events.clear(); mocks.enqueue.mockReset(); mocks.run.mockReset(); mocks.pause.mockReset(); mocks.resume.mockReset(); mocks.retry.mockReset(); mocks.policy.mockReset(); mocks.list.mockReset(); mocks.materialize.mockReset(); mocks.release.mockReset(); mocks.state = state(); Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' }); stop = activatePhotoBackup(); });
    afterEach(() => { stop(); });

    it('automatically drains multiple bounded pages once', async () => {
        mocks.list.mockResolvedValueOnce({ assets: [{ id: '1', version: '1', name: 'a', mediaType: 'photo', modifiedAt: 1, size: 1 }], nextCursor: 'next' }).mockResolvedValueOnce({ assets: [{ id: '2', version: '1', name: 'b', mediaType: 'photo', modifiedAt: 2, size: 1 }], nextCursor: '' });
        await startPhotoBackup(); await flush();
        expect(mocks.enqueue).toHaveBeenCalledTimes(2);
        mocks.events.get('photo-backup:state')?.({}); await flush();
        expect(mocks.enqueue).toHaveBeenCalledTimes(2);
    });

    it('keeps an explicit pause through native resume events', async () => {
        mocks.list.mockResolvedValue({ assets: [], nextCursor: '' });
        mocks.pause.mockImplementationOnce(() => { mocks.state = { ...state(), status: { ...state().status, phase: 'paused', message: 'Paused by you.' }, manualPaused: true }; });
        await pausePhotoBackupNow();
        mocks.list.mockClear();
        mocks.events.get('android:PhotoBackupMediaChanged')?.({}); await flush();
        expect(mocks.list).not.toHaveBeenCalled();
    });

    it('honors a durable backend pause after controller activation', async () => {
        stop();
        mocks.state = { ...state(), status: { ...state().status, phase: 'paused', message: 'Paused by you.' }, manualPaused: true };
        mocks.list.mockResolvedValue({ assets: [], nextCursor: '' });
        mocks.run.mockClear(); mocks.list.mockClear();
        stop = activatePhotoBackup(); await flush();
        mocks.events.get('android:PhotoBackupMediaChanged')?.({}); await flush();
        expect(mocks.run).not.toHaveBeenCalled();
        expect(mocks.list).not.toHaveBeenCalled();
    });

    it('does not let retry implicitly resume a manual pause', async () => {
        mocks.state = { ...state(), status: { ...state().status, phase: 'paused', message: 'Paused by you.' }, manualPaused: true };
        mocks.list.mockResolvedValue({ assets: [], nextCursor: '' });
        await flush(); mocks.run.mockClear(); mocks.list.mockClear();
        await retryPhotoBackupNow(); await flush();
        expect(mocks.retry).toHaveBeenCalledOnce();
        expect(mocks.resume).not.toHaveBeenCalled();
        expect(mocks.run).not.toHaveBeenCalled();
        expect(mocks.list).not.toHaveBeenCalled();
    });

    it('resumes explicitly and restarts the scheduler', async () => {
        mocks.list.mockResolvedValue({ assets: [], nextCursor: '' });
        await pausePhotoBackupNow();
        await resumePhotoBackupNow(); await flush();
        expect(mocks.resume).toHaveBeenCalledOnce();
        expect(mocks.run).toHaveBeenCalled();
        expect(mocks.list).toHaveBeenCalled();
    });

    it('shows feedback when pausing fails', async () => {
        mocks.pause.mockRejectedValueOnce(new Error('offline'));
        await expect(pausePhotoBackupNow()).resolves.toBeUndefined();
        expect(get(photoBackupError)).toBe('Could not pause photo backup. Try again.');
    });

    it('does not start discovery while hidden', async () => {
        stop(); await flush(); mocks.list.mockClear(); Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' }); stop = activatePhotoBackup();
        mocks.list.mockResolvedValue({ assets: [], nextCursor: '' }); await startPhotoBackup(); await flush();
        expect(mocks.list).not.toHaveBeenCalled();
    });

    it('releases a late native stage when Go already released its token', async () => {
        let finish!: (value: { path: string; releaseID: string }) => void;
        mocks.materialize.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
        mocks.events.get('photo-backup:materialize')?.({ token: 't', asset: { id: 'a', version: '1', name: 'a', media_type: 'photo' } });
        mocks.events.get('photo-backup:release')?.({ token: 't', asset: { id: 'a', version: '1', media_type: 'photo' } });
        finish({ path: '/tmp/a', releaseID: 'native-t' }); await flush();
        expect(mocks.release).toHaveBeenCalledWith('native-t');
    });

    it('does not enqueue a provider page that finishes after pause', async () => {
        let finish!: (value: unknown) => void;
        mocks.list.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
        await startPhotoBackup(); await flush();
        await pausePhotoBackupNow();
        finish({ assets: [{ id: 'late', version: '1', name: 'late.jpg', mediaType: 'photo' }], nextCursor: '' });
        await flush();
        expect(mocks.enqueue).not.toHaveBeenCalled();
    });
});
