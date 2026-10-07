// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';
import type { PhotoBackupAsset, PhotoBackupState } from '../../api/photo-backup';
import type { OperationResult } from '../../types';

const mocks = vi.hoisted(() => ({
    events: new Map<string, (payload: unknown) => void>(),
    state: null as PhotoBackupState | null,
    getState: vi.fn(), save: vi.fn(),
    commit: vi.fn(), resetScan: vi.fn(), run: vi.fn(), pause: vi.fn(), resume: vi.fn(), retry: vi.fn(), policy: vi.fn(), unlock: vi.fn(),
    list: vi.fn(), materialize: vi.fn(), release: vi.fn(), nativePolicy: vi.fn(),
    upsert: vi.fn(), pickFolder: vi.fn(), addFolder: vi.fn(), requestAccess: vi.fn(),
}));

const ok: OperationResult = { ok: true };
const locked: OperationResult = { ok: false, error: { code: 'encryption_password_required', message: 'Enter your encryption password first.' } };
const waitingForWiFi: OperationResult = { ok: false, error: { code: 'operation_failed', message: 'Waiting for Wi-Fi.' } };

vi.mock('../../api/photo-backup', () => ({
    defaultSettings: { enabled: false, photos: true, videos: true, wifiOnly: false, encrypt: false },
    normalizeAsset: (value: unknown): PhotoBackupAsset | null => {
        const raw = value as Record<string, unknown>;
        return raw?.id ? { id: String(raw.id), version: String(raw.version), name: String(raw.name ?? ''), mediaType: raw.media_type === 'video' ? 'video' : 'photo', modifiedAt: 0, createdAt: Number(raw.created_at) || 0, size: Number(raw.size) || 0 } : null;
    },
    getPhotoBackupState: mocks.getState,
    commitPhotoBackupScanPage: mocks.commit, resetPhotoBackupScan: mocks.resetScan,
    runPhotoBackup: mocks.run,
    pausePhotoBackup: mocks.pause,
    resumePhotoBackup: mocks.resume,
    setPhotoBackupPolicy: mocks.policy,
    savePhotoBackupSettings: mocks.save, addPhotoBackupFolder: mocks.addFolder, removePhotoBackupSource: vi.fn(), retryPhotoBackup: mocks.retry, resolvePhotoBackupResource: vi.fn(), upsertPhotoBackupSource: mocks.upsert,
}));
vi.mock('../../api/runtime', () => ({ runtimeEventsAvailable: () => true, onRuntimeEvent: (name: string, cb: (payload: unknown) => void) => { mocks.events.set(name, cb); return () => mocks.events.delete(name); } }));
vi.mock('./native-adapter', () => ({
    nativePhotoBackupAvailable: () => true,
    nativePhotoBackupPolicy: mocks.nativePolicy,
    requestNativePhotoBackupAccess: mocks.requestAccess,
    nativePhotoBackupFolderPicking: () => true, pickNativePhotoBackupFolder: mocks.pickFolder,
    listNativePhotoBackupAssets: mocks.list, materializeNativePhotoBackupAsset: mocks.materialize, releaseNativePhotoBackupAsset: mocks.release,
}));
vi.mock('../encryption', () => ({ requireEncryptionPassword: mocks.unlock }));
vi.mock('../errors', () => ({ humanizeBackendError: (error: unknown) => String((error as { message?: string })?.message ?? '') }));

import { activatePhotoBackup, choosePhotoBackupFolder, pausePhotoBackupNow, photoBackupAccessNote, photoBackupError, photoBackupState, refreshPhotoBackup, resumePhotoBackupNow, retryPhotoBackupNow, startPhotoBackup, updatePhotoBackupSettings } from './controller';
import { activeTransfers } from '../../ui/notifications/notif-store';
import { sidebarState } from '../../ui/sidebar/sidebar-store';

const flush = async () => { for (let i = 0; i < 8; i += 1) await new Promise((resolve) => setTimeout(resolve, 0)); };
const state = (): PhotoBackupState => ({ settings: { enabled: true, photos: true, videos: true, wifiOnly: false, encrypt: true }, sources: [{ id: 'tree:external_primary:DCIM/', kind: 'device-folder', root: 'external_primary:DCIM/', name: 'DCIM', enabled: true, addedAt: 0 }], status: { phase: 'idle', pending: 0, uploading: 0, complete: 0, failed: 0, paused: 0, bytesDone: 0, bytesTotal: 0, currentFile: '', currentFileBytesDone: 0, currentFileBytesTotal: 0, currentFilePercent: 0, message: '' }, capabilities: { wifiOnly: { supported: true, label: '', detail: '' }, access: { status: 'granted', detail: '' }, }, platform: 'android', destination: { id: '1', title: 'Personal', kind: 'personal' }, manualPaused: false, encryptionRequired: false });
const nativeStageEvent = (id: string, asset?: unknown): Record<string, unknown> => Object.fromEntries(asset === undefined ? [['token', id]] : [['token', id], ['asset', asset]]);

describe('photo backup controller scheduler', () => {
    let stop = () => {};
    beforeEach(() => {
        stop(); mocks.events.clear();
        for (const mock of [mocks.commit, mocks.resetScan, mocks.run, mocks.pause, mocks.resume, mocks.retry, mocks.policy, mocks.unlock, mocks.list, mocks.materialize, mocks.release, mocks.getState, mocks.save]) mock.mockReset();
        for (const control of [mocks.run, mocks.pause, mocks.resume, mocks.retry]) control.mockResolvedValue(ok);
        // The host answers every access request with the grant it gave.
        mocks.requestAccess.mockReset().mockResolvedValue({ status: 'granted', detail: '' });
        mocks.unlock.mockResolvedValue(true);
        mocks.nativePolicy.mockReset().mockResolvedValue(true);
        mocks.state = state(); mocks.getState.mockImplementation(() => Promise.resolve(mocks.state));
        mocks.save.mockImplementation(async (settings) => ({ ...state(), settings }));
        sidebarState.set({ personal: [], shared: [], pending: [], activeChannelId: null, virtualView: null });
        Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
        stop = activatePhotoBackup();
    });
    afterEach(() => { stop(); vi.useRealTimers(); });

    it('automatically drains multiple bounded pages once', async () => {
        mocks.list.mockResolvedValueOnce({ assets: [{ id: '1', version: '1', name: 'a', mediaType: 'photo', modifiedAt: 1, createdAt: 0, size: 1 }], nextCursor: 'next' }).mockResolvedValueOnce({ assets: [{ id: '2', version: '1', name: 'b', mediaType: 'photo', modifiedAt: 2, createdAt: 0, size: 1 }], nextCursor: '' });
        await startPhotoBackup(); await flush();
        expect(mocks.commit).toHaveBeenCalledTimes(2);
        mocks.events.get('photo-backup:state')?.({}); await flush();
        expect(mocks.commit).toHaveBeenCalledTimes(2);
    });

    it('discovers every asset across the 128-item page boundary', async () => {
        stop(); await flush(); mocks.list.mockClear();
        const assets = Array.from({ length: 1000 }, (_, index) => ({ id: String(index), version: '1', name: `${index}.jpg`, mediaType: 'photo' as const, modifiedAt: index, createdAt: 0, size: 1 }));
        mocks.list.mockImplementation(async (_source: string, cursor: string) => {
            const start = cursor ? Number(cursor) : 0;
            const end = Math.min(start + 128, assets.length);
            return { assets: assets.slice(start, end), nextCursor: end < assets.length ? String(end) : '' };
        });

        stop = activatePhotoBackup(); await flush();

        expect(mocks.list).toHaveBeenCalledTimes(8);
        expect(mocks.commit).toHaveBeenCalledTimes(8);
        expect(mocks.commit.mock.calls.flatMap((call) => call[3])).toHaveLength(1000);
        expect(mocks.commit.mock.calls.map((call) => [call[1], call[2]])).toEqual([
            ['', '128'], ['128', '256'], ['256', '384'], ['384', '512'],
            ['512', '640'], ['640', '768'], ['768', '896'], ['896', ''],
        ]);
        expect(mocks.list.mock.calls.map((call) => call[1])).toEqual(['', '128', '256', '384', '512', '640', '768', '896']);
    });

    it('resumes from a durable cursor after controller activation', async () => {
        stop(); await flush(); mocks.list.mockClear();
        mocks.state = { ...state(), sources: [{ ...state().sources[0], scanCursor: '128', scanComplete: false }] };
        mocks.list.mockResolvedValue({ assets: [{ id: '129', version: '1', name: '129.jpg', mediaType: 'photo' }], nextCursor: '' });

        stop = activatePhotoBackup(); await flush();

        expect(mocks.list).toHaveBeenCalledWith(mocks.state.sources[0].id, '128');
        expect(mocks.commit).toHaveBeenCalledWith(mocks.state.sources[0].id, '128', '', expect.arrayContaining([expect.objectContaining({ id: '129' })]));
        expect(mocks.resetScan).not.toHaveBeenCalled();
    });

    it('resets a completed scan before checking for newly added media', async () => {
        stop(); await flush(); mocks.list.mockClear();
        mocks.state = { ...state(), sources: [{ ...state().sources[0], scanCursor: '', scanComplete: true }] };
        mocks.list.mockResolvedValue({ assets: [{ id: 'new', version: '1', name: 'new.jpg', mediaType: 'photo' }], nextCursor: '' });

        stop = activatePhotoBackup(); await flush();

        expect(mocks.resetScan).toHaveBeenCalledWith(mocks.state.sources[0].id);
        expect(mocks.commit).toHaveBeenCalledWith(mocks.state.sources[0].id, '', '', expect.arrayContaining([expect.objectContaining({ id: 'new' })]));
    });

    it('restarts discovery after visibility returns while an old page is pending', async () => {
        stop(); await flush(); mocks.list.mockClear();
        let finishOldPage!: (value: unknown) => void;
        const assets = Array.from({ length: 1000 }, (_, index) => ({ id: String(index), version: '1', name: `${index}.jpg`, mediaType: 'photo' as const, modifiedAt: index, createdAt: 0, size: 1 }));
        mocks.list.mockImplementationOnce(() => new Promise((resolve) => { finishOldPage = resolve; }));
        mocks.list.mockImplementation(async (_source: string, cursor: string) => {
            const start = cursor ? Number(cursor) : 0;
            const end = Math.min(start + 128, assets.length);
            return { assets: assets.slice(start, end), nextCursor: end < assets.length ? String(end) : '' };
        });

        stop = activatePhotoBackup(); await flush();
        expect(mocks.list).toHaveBeenCalledTimes(1);
        Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' });
        document.dispatchEvent(new Event('visibilitychange'));
        Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
        document.dispatchEvent(new Event('visibilitychange'));

        finishOldPage({ assets: [{ id: 'stale', version: '1', name: 'stale.jpg', mediaType: 'photo' }], nextCursor: '' });
        await flush();

        expect(mocks.list).toHaveBeenCalledTimes(9);
        expect(mocks.commit.mock.calls.flatMap((call) => call[3])).toHaveLength(1000);
        expect(mocks.commit.mock.calls.flatMap((call) => call[3]).some((asset) => asset.id === 'stale')).toBe(false);
    });

    it('restarts discovery after a media change while an old page is pending', async () => {
        stop(); await flush(); mocks.list.mockClear();
        let finishOldPage!: (value: unknown) => void;
        mocks.list.mockImplementationOnce(() => new Promise((resolve) => { finishOldPage = resolve; }));
        mocks.list.mockResolvedValue({ assets: [{ id: 'new', version: '1', name: 'new.jpg', mediaType: 'photo' }], nextCursor: '' });

        stop = activatePhotoBackup(); await flush();
        mocks.events.get('android:PhotoBackupMediaChanged')?.({});
        finishOldPage({ assets: [{ id: 'stale', version: '1', name: 'stale.jpg', mediaType: 'photo' }], nextCursor: '' });
        await flush();

        expect(mocks.list).toHaveBeenCalledTimes(2);
        expect(mocks.commit).toHaveBeenCalledOnce();
        expect(mocks.commit.mock.calls[0][3]).toMatchObject([{ id: 'new' }]);
    });

    it('rechecks the start after media changes during an incomplete Android scan', async () => {
        stop(); await flush(); mocks.list.mockClear();
        mocks.state = { ...state(), sources: [{ ...state().sources[0], scanCursor: '128', scanComplete: false }] };
        let finishOldPage!: (value: unknown) => void;
        mocks.list.mockImplementationOnce(() => new Promise((resolve) => { finishOldPage = resolve; }));
        mocks.list.mockImplementation(async (_source: string, cursor: string) => ({
            assets: [{ id: cursor ? 'older' : 'newer', version: '1', name: 'photo.jpg', mediaType: 'photo' }], nextCursor: '',
        }));
        mocks.commit.mockImplementation(async (_source: string, _previous: string, next: string) => {
            mocks.state = { ...mocks.state!, sources: [{ ...mocks.state!.sources[0], scanCursor: next, scanComplete: next === '' }] };
        });
        mocks.resetScan.mockImplementation(async () => {
            mocks.state = { ...mocks.state!, sources: [{ ...mocks.state!.sources[0], scanCursor: '', scanComplete: false }] };
        });

        stop = activatePhotoBackup(); await flush();
        mocks.events.get('android:PhotoBackupMediaChanged')?.({});
        finishOldPage({ assets: [], nextCursor: '' });
        await flush();

        expect(mocks.list.mock.calls.map((call) => call[1])).toEqual(['128', '128', '']);
        expect(mocks.resetScan).toHaveBeenCalledOnce();
        expect(mocks.commit.mock.calls.flatMap((call) => call[3]).map((asset) => asset.id)).toEqual(['older', 'newer']);
    });

    it('reports scanning while the library is being walked, and only then', async () => {
        let finish!: (value: unknown) => void;
        mocks.list.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
        await startPhotoBackup(); await flush();
        expect(get(photoBackupState)?.status.phase).toBe('scanning');
        expect(get(activeTransfers)[0]).toMatchObject({ name: 'Scanning photos and videos' });
        finish({ assets: [], nextCursor: '' }); await flush();
        expect(get(photoBackupState)?.status.phase).toBe('idle');
    });

    it('keeps an explicit pause through native resume events', async () => {
        mocks.list.mockResolvedValue({ assets: [], nextCursor: '' });
        mocks.pause.mockImplementationOnce(async () => { mocks.state = { ...state(), status: { ...state().status, phase: 'paused', message: 'Paused by you.' }, manualPaused: true }; return ok; });
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

    it('shows generic feedback when the bridge itself fails to pause', async () => {
        let stageSignal: AbortSignal | undefined;
        mocks.materialize.mockImplementationOnce((_asset, signal: AbortSignal) => {
            stageSignal = signal;
            return new Promise((_resolve, reject) => {
                signal.addEventListener('abort', () => reject(new Error('cancelled')), { once: true });
            });
        });
        mocks.events.get('photo-backup:materialize')?.({ token: 'active', asset: { id: 'a', version: '1', name: 'a', media_type: 'photo' } });
        await Promise.resolve();
        mocks.pause.mockRejectedValueOnce(new Error('offline'));
        await expect(pausePhotoBackupNow()).resolves.toBeUndefined();
        expect(stageSignal?.aborted).toBe(false);
        expect(get(photoBackupError)).toBe('Could not pause photo backup. Try again.');
    });

    it("repeats the backend's own reason when it declines to start", async () => {
        stop(); mocks.run.mockResolvedValue(waitingForWiFi);
        await startPhotoBackup();
        expect(get(photoBackupError)).toBe('Waiting for Wi-Fi.');
        expect(mocks.unlock).not.toHaveBeenCalled();
    });

    it('unlocks and retries an explicit backup once when the vault is locked', async () => {
        stop(); mocks.run.mockReset().mockResolvedValueOnce(locked).mockResolvedValueOnce(ok);
        await startPhotoBackup();
        expect(mocks.unlock).toHaveBeenCalledOnce();
        expect(mocks.run).toHaveBeenCalledTimes(2);
        expect(get(photoBackupError)).toBe('');
    });

    it('does not start an explicitly locked backup when unlocking is cancelled', async () => {
        stop(); mocks.run.mockClear();
        mocks.state = { ...state(), encryptionRequired: true };
        mocks.unlock.mockResolvedValueOnce(false);
        await refreshPhotoBackup();
        await startPhotoBackup();
        expect(mocks.unlock).toHaveBeenCalledOnce();
        expect(mocks.run).not.toHaveBeenCalled();
        expect(get(photoBackupError)).toBe('');
    });

    it('does not prompt from the automatic scheduler when encryption is locked', async () => {
        await flush(); mocks.run.mockClear();
        mocks.state = { ...state(), encryptionRequired: true };
        mocks.events.get('android:PhotoBackupMediaChanged')?.({});
        await flush();
        expect(mocks.unlock).not.toHaveBeenCalled();
        expect(mocks.run).not.toHaveBeenCalled();
        expect(get(photoBackupError)).toBe('Unlock encryption to continue photo backup.');
    });

    it('keeps the backend reason when encryption status could not be verified', async () => {
        await flush(); mocks.run.mockClear();
        const message = 'Could not check encryption. Try again when connected.';
        mocks.state = { ...state(), encryptionRequired: true, status: { ...state().status, phase: 'paused', message } };
        mocks.events.get('android:PhotoBackupMediaChanged')?.({});
        await flush();

        expect(mocks.unlock).not.toHaveBeenCalled();
        expect(mocks.run).not.toHaveBeenCalled();
        expect(get(photoBackupError)).toBe(message);
    });

    it('does not prompt when the backend reports a locked vault to the automatic scheduler', async () => {
        await flush(); mocks.list.mockClear();
        mocks.run.mockResolvedValue(locked);
        mocks.events.get('android:PhotoBackupMediaChanged')?.({});
        await flush();
        expect(mocks.unlock).not.toHaveBeenCalled();
        expect(mocks.list).not.toHaveBeenCalled();
        expect(get(photoBackupError)).toBe('Unlock encryption to continue photo backup.');
    });

    it('does not start discovery while hidden', async () => {
        stop(); await flush(); mocks.list.mockClear(); Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' }); stop = activatePhotoBackup();
        mocks.list.mockResolvedValue({ assets: [], nextCursor: '' }); await startPhotoBackup(); await flush();
        expect(mocks.list).not.toHaveBeenCalled();
    });

    it('releases a late native stage when Go already released its token', async () => {
        let finish!: (value: { path: string; releaseID: string }) => void;
        mocks.materialize.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
        mocks.events.get('photo-backup:materialize')?.(nativeStageEvent('t', { id: 'a', version: '1', name: 'a', media_type: 'photo' }));
        await Promise.resolve();
        mocks.events.get('photo-backup:release')?.(nativeStageEvent('t', { id: 'a', version: '1', media_type: 'photo' }));
        finish({ path: '/tmp/a', releaseID: 'native-t' }); await flush();
        expect(mocks.release).toHaveBeenCalledWith('native-t');
    });

    it('stages two assets before either upload releases its source, but waits for cleanup before a third', async () => {
        let finishRelease!: () => void;
        mocks.materialize
            .mockResolvedValueOnce({ path: '/tmp/a', releaseID: 'native-a' })
            .mockResolvedValueOnce({ path: '/tmp/b', releaseID: 'native-b' })
            .mockResolvedValueOnce({ path: '/tmp/c', releaseID: 'native-c' });
        mocks.release.mockReturnValueOnce(new Promise<void>((resolve) => { finishRelease = resolve; }));

        mocks.events.get('photo-backup:materialize')?.(nativeStageEvent('first', { id: 'a', version: '1', name: 'a', media_type: 'photo' }));
        await flush();
        mocks.events.get('photo-backup:materialize')?.(nativeStageEvent('second', { id: 'b', version: '1', name: 'b', media_type: 'photo' }));
        await flush();
        expect(mocks.materialize).toHaveBeenCalledTimes(2);
        mocks.events.get('photo-backup:release')?.(nativeStageEvent('first'));
        mocks.events.get('photo-backup:materialize')?.(nativeStageEvent('third', { id: 'c', version: '1', name: 'c', media_type: 'photo' }));
        await flush();
        expect(mocks.materialize).toHaveBeenCalledTimes(2);
        finishRelease();
        await flush();
        expect(mocks.materialize).toHaveBeenCalledTimes(3);
    });

    it('bounds overlapping native stages by bytes, not just file count', async () => {
        mocks.materialize.mockResolvedValue({ path: '/tmp/video', releaseID: 'video' });
        const asset = { id: 'a', version: '1', name: 'video', media_type: 'video', size: 3 * 1024 ** 3 };
        mocks.events.get('photo-backup:materialize')?.(nativeStageEvent('large-first', asset));
        mocks.events.get('photo-backup:materialize')?.(nativeStageEvent('large-second', { ...asset, id: 'b' }));
        await flush();
        expect(mocks.materialize).toHaveBeenCalledTimes(1);
        mocks.events.get('photo-backup:release')?.(nativeStageEvent('large-first'));
        await flush();
        expect(mocks.materialize).toHaveBeenCalledTimes(2);
    });

    it('ignores duplicate tokens and cancels queued stages without materializing them', async () => {
        mocks.materialize.mockResolvedValue({ path: '/tmp/video', releaseID: 'video' });
        const asset = { id: 'a', version: '1', name: 'video', media_type: 'video', size: 3 * 1024 ** 3 };
        mocks.events.get('photo-backup:materialize')?.(nativeStageEvent('held', asset));
        mocks.events.get('photo-backup:materialize')?.(nativeStageEvent('held', asset));
        mocks.events.get('photo-backup:materialize')?.(nativeStageEvent('canceled', { ...asset, id: 'b' }));
        await flush();
        mocks.events.get('photo-backup:release')?.(nativeStageEvent('canceled'));
        mocks.events.get('photo-backup:release')?.(nativeStageEvent('held'));
        await flush();
        expect(mocks.materialize).toHaveBeenCalledTimes(1);
        expect(mocks.release).toHaveBeenCalledTimes(1);
    });

    it('refreshes Wi-Fi during long uploads and stops polling when hidden or disposed', async () => {
        stop(); await flush(); vi.useFakeTimers();
        mocks.list.mockResolvedValue({ assets: [], nextCursor: '' });
        mocks.state = { ...state(), settings: { ...state().settings, wifiOnly: true }, status: { ...state().status, phase: 'uploading', uploading: 1 } };
        stop = activatePhotoBackup();
        await vi.advanceTimersByTimeAsync(0);
        mocks.policy.mockClear(); mocks.run.mockClear();
        await vi.advanceTimersByTimeAsync(150_000);
        expect(mocks.policy).toHaveBeenCalledTimes(5);
        expect(mocks.run).not.toHaveBeenCalled();
        Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' });
        document.dispatchEvent(new Event('visibilitychange'));
        await vi.advanceTimersByTimeAsync(90_000);
        expect(mocks.policy).toHaveBeenCalledTimes(5);
        stop();
        await vi.advanceTimersByTimeAsync(90_000);
        expect(mocks.policy).toHaveBeenCalledTimes(5);
    });

    it('periodically restarts policy-waiting work without overriding manual pause', async () => {
        stop(); await flush(); vi.useFakeTimers();
        mocks.list.mockResolvedValue({ assets: [], nextCursor: '' });
        mocks.state = { ...state(), settings: { ...state().settings, wifiOnly: true }, status: { ...state().status, phase: 'paused', pending: 1 } };
        stop = activatePhotoBackup(); await vi.advanceTimersByTimeAsync(0);
        mocks.run.mockClear();
        await vi.advanceTimersByTimeAsync(30_000);
        expect(mocks.run).toHaveBeenCalled();
        mocks.state = { ...mocks.state, manualPaused: true };
        await refreshPhotoBackup(); mocks.run.mockClear(); mocks.policy.mockClear();
        await vi.advanceTimersByTimeAsync(60_000);
        expect(mocks.run).not.toHaveBeenCalled();
        expect(mocks.policy).not.toHaveBeenCalled();
        expect(mocks.resume).not.toHaveBeenCalled();
    });

    it('does not publish a policy sample that finishes after disposal', async () => {
        stop(); await flush(); vi.useFakeTimers();
        mocks.list.mockResolvedValue({ assets: [], nextCursor: '' });
        mocks.state = { ...state(), settings: { ...state().settings, wifiOnly: true } };
        stop = activatePhotoBackup(); await vi.advanceTimersByTimeAsync(0);
        let finish!: (wifi: boolean) => void;
        mocks.nativePolicy.mockReturnValueOnce(new Promise<boolean>((resolve) => { finish = resolve; }));
        mocks.policy.mockClear();
        await vi.advanceTimersByTimeAsync(30_000);
        stop(); finish(true);
        await vi.advanceTimersByTimeAsync(60_000);
        expect(mocks.policy).not.toHaveBeenCalled();
    });

    it('continues staging after native cleanup reports an error', async () => {
        mocks.materialize
            .mockResolvedValueOnce({ path: '/tmp/a', releaseID: 'native-a' })
            .mockResolvedValueOnce({ path: '/tmp/b', releaseID: 'native-b' });
        mocks.release.mockRejectedValueOnce(new Error('cleanup failed'));

        mocks.events.get('photo-backup:materialize')?.(nativeStageEvent('first', { id: 'a', version: '1', name: 'a', media_type: 'photo' }));
        await flush();
        mocks.events.get('photo-backup:release')?.(nativeStageEvent('first'));
        mocks.events.get('photo-backup:materialize')?.(nativeStageEvent('second', { id: 'b', version: '1', name: 'b', media_type: 'photo' }));
        await flush();

        expect(mocks.materialize).toHaveBeenCalledTimes(2);
        expect(get(photoBackupError)).toBe('Temporary media cleanup failed. Reopen TDrive to retry cleanup.');
    });

    it('asks for encryption before enabling backup and leaves it off when setup is cancelled', async () => {
        const disabled = { ...state(), settings: { ...state().settings, enabled: false } };
        mocks.state = disabled;
        mocks.getState.mockResolvedValue(disabled);
        mocks.unlock.mockResolvedValueOnce(false);
        await refreshPhotoBackup();

        await updatePhotoBackupSettings({ ...disabled.settings, enabled: true, encrypt: false });

        expect(mocks.unlock).toHaveBeenCalledOnce();
        expect(mocks.save).not.toHaveBeenCalled();
    });

    it('forces encrypted settings after setup succeeds', async () => {
        const disabled = { ...state(), settings: { ...state().settings, enabled: false } };
        mocks.state = disabled;
        mocks.getState.mockResolvedValue(disabled);
        await refreshPhotoBackup();

        await updatePhotoBackupSettings({ ...disabled.settings, enabled: true, encrypt: false });

        expect(mocks.save).toHaveBeenCalledWith(expect.objectContaining({ enabled: true, encrypt: true }));
    });

    it('refuses shared-drive backup before prompting or staging media', async () => {
        const disabled = { ...state(), settings: { ...state().settings, enabled: false } };
        mocks.state = disabled;
        mocks.getState.mockResolvedValue(disabled);
        sidebarState.set({ personal: [], shared: [{ id: 2, title: 'Team', kind: 'shared' } as never], pending: [], activeChannelId: 2, virtualView: null });
        await refreshPhotoBackup();

        await updatePhotoBackupSettings({ ...disabled.settings, enabled: true });

        expect(mocks.unlock).not.toHaveBeenCalled();
        expect(mocks.save).not.toHaveBeenCalled();
        expect(get(photoBackupError)).toBe('Encrypted photo backup is available only in My Drive.');
    });

    it('does not enqueue a provider page that finishes after pause', async () => {
        let finish!: (value: unknown) => void;
        mocks.list.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
        await startPhotoBackup(); await flush();
        await pausePhotoBackupNow();
        finish({ assets: [{ id: 'late', version: '1', name: 'late.jpg', mediaType: 'photo' }], nextCursor: '' });
        await flush();
        expect(mocks.commit).not.toHaveBeenCalled();
    });

    it('does not reset a completed checkpoint after discovery is canceled during queue drain', async () => {
        stop(); await flush(); mocks.list.mockClear();
        mocks.state = { ...state(), sources: [{ ...state().sources[0], scanCursor: '', scanComplete: true }] };
        let finishDrain!: (result: OperationResult) => void;
        mocks.run.mockImplementationOnce(() => new Promise<OperationResult>((resolve) => { finishDrain = resolve; }));
        stop = activatePhotoBackup(); await flush();
        Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' });
        document.dispatchEvent(new Event('visibilitychange'));
        finishDrain(ok); await flush();

        expect(mocks.resetScan).not.toHaveBeenCalled();
        expect(mocks.list).not.toHaveBeenCalled();
    });

    it('refetches backup activity for the initial drive scope and ignores the earlier stale reply', async () => {
        stop();
        const old = { ...state(), status: { ...state().status, phase: 'uploading' as const, currentFile: 'old-private.jpg', currentFileBytesDone: 20, currentFileBytesTotal: 100, currentFilePercent: 20 } };
        const current = { ...state(), status: { ...state().status, phase: 'uploading' as const, currentFile: 'current.jpg', currentFileBytesDone: 50, currentFileBytesTotal: 100, currentFilePercent: 50 } };
        let resolveOld!: (value: PhotoBackupState) => void;
        mocks.getState.mockReset()
            .mockImplementationOnce(() => new Promise<PhotoBackupState>((resolve) => { resolveOld = resolve; }))
            .mockResolvedValue(current);
        sidebarState.set({ personal: [{ id: 9, title: 'Current', kind: 'personal' } as never], shared: [], pending: [], activeChannelId: 9, virtualView: null });
        stop = activatePhotoBackup();
        await flush();
        // The row names the run; the file it is on is the one listed under it,
        // so that is where a stale reply would show up.
        expect(get(activeTransfers)[0].items).toMatchObject([{ name: 'current.jpg', progress: 50 }]);
        resolveOld(old); await flush();
        expect(get(activeTransfers)[0].items).toMatchObject([{ name: 'current.jpg', progress: 50 }]);
    });

    it('adds a folder the host picked, and says nothing when the picker was dismissed', async () => {
        mocks.pickFolder.mockReset().mockResolvedValueOnce({ id: 'tree:external_primary:DCIM/Camera/', kind: 'device-folder', name: 'Camera', root: 'external_primary:DCIM/Camera/', enabled: true, addedAt: 0 });
        mocks.upsert.mockReset().mockResolvedValue(undefined);
        await choosePhotoBackupFolder();
        expect(mocks.upsert).toHaveBeenCalledWith(expect.objectContaining({ id: 'tree:external_primary:DCIM/Camera/', kind: 'device-folder' }));
        // A phone never reaches the backend's own dialog.
        expect(mocks.addFolder).not.toHaveBeenCalled();
        mocks.upsert.mockClear();
        mocks.pickFolder.mockResolvedValueOnce(null);
        await choosePhotoBackupFolder();
        expect(mocks.upsert).not.toHaveBeenCalled();
        expect(get(photoBackupError)).toBe('');
    });

    it('shows the host refusal for a folder it cannot read, without its log prefix', async () => {
        mocks.pickFolder.mockReset().mockRejectedValueOnce(new Error('photo backup: "Camera" is already covered by "DCIM".'));
        await choosePhotoBackupFolder();
        expect(get(photoBackupError)).toBe('"Camera" is already covered by "DCIM".');
    });

    // Adding a folder is the moment the media grant matters: on a phone the
    // folder is read through the library, so both permissions are needed and
    // a partial grant is worth saying out loud next to the folder it limits.
    it('asks for the media grant when a folder is added, and explains a partial one', async () => {
        mocks.requestAccess.mockReset().mockResolvedValue({ status: 'limited', detail: 'TDrive can only see the photos you picked.' });
        await choosePhotoBackupFolder();
        expect(mocks.requestAccess).toHaveBeenCalled();
        expect(get(photoBackupAccessNote)).toBe('TDrive can only see the photos you picked.');
        // Full access is the expected case and says nothing.
        mocks.requestAccess.mockResolvedValue({ status: 'granted', detail: 'full media access' });
        await choosePhotoBackupFolder();
        expect(get(photoBackupAccessNote)).toBe('');
    });

    it('does not restore a backup activity when a refresh resolves after disposal', async () => {
        stop();
        let resolve!: (value: PhotoBackupState) => void;
        mocks.getState.mockReset().mockImplementationOnce(() => new Promise<PhotoBackupState>((done) => { resolve = done; }));
        stop = activatePhotoBackup();
        stop();
        resolve({ ...state(), status: { ...state().status, phase: 'uploading', currentFile: 'private.jpg', currentFileBytesDone: 50, currentFileBytesTotal: 100, currentFilePercent: 50 } });
        await flush();
        expect(get(activeTransfers)).toEqual([]);
    });
});
