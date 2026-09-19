// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';
import type { PhotoBackupAsset, PhotoBackupState } from '../../api/photo-backup';
import type { OperationResult } from '../../types';

const mocks = vi.hoisted(() => ({
    events: new Map<string, (payload: unknown) => void>(),
    state: null as PhotoBackupState | null,
    getState: vi.fn(),
    enqueue: vi.fn(), run: vi.fn(), pause: vi.fn(), resume: vi.fn(), retry: vi.fn(), policy: vi.fn(), unlock: vi.fn(), prompt: vi.fn(),
    list: vi.fn(), materialize: vi.fn(), release: vi.fn(),
    upsert: vi.fn(), pickFolder: vi.fn(), addFolder: vi.fn(), requestAccess: vi.fn(),
}));

const ok: OperationResult = { ok: true };
const locked: OperationResult = { ok: false, error: { code: 'encryption_password_required', message: 'Enter your encryption password first.' } };
const waitingForWiFi: OperationResult = { ok: false, error: { code: 'operation_failed', message: 'Waiting for Wi-Fi.' } };

vi.mock('../../api/photo-backup', () => ({
    defaultSettings: { enabled: false, photos: true, videos: true, wifiOnly: false, encrypt: false },
    normalizeAsset: (value: unknown): PhotoBackupAsset | null => {
        const raw = value as Record<string, unknown>;
        return raw?.id ? { id: String(raw.id), version: String(raw.version), name: String(raw.name ?? ''), mediaType: raw.media_type === 'video' ? 'video' : 'photo', modifiedAt: 0, createdAt: Number(raw.created_at) || 0, size: 0 } : null;
    },
    getPhotoBackupState: mocks.getState,
    enqueuePhotoBackupAssets: mocks.enqueue,
    runPhotoBackup: mocks.run,
    pausePhotoBackup: mocks.pause,
    resumePhotoBackup: mocks.resume,
    setPhotoBackupPolicy: mocks.policy,
    savePhotoBackupSettings: vi.fn(), addPhotoBackupFolder: mocks.addFolder, removePhotoBackupSource: vi.fn(), retryPhotoBackup: mocks.retry, resolvePhotoBackupResource: vi.fn(), upsertPhotoBackupSource: mocks.upsert,
}));
vi.mock('../../api/runtime', () => ({ runtimeEventsAvailable: () => true, onRuntimeEvent: (name: string, cb: (payload: unknown) => void) => { mocks.events.set(name, cb); return () => mocks.events.delete(name); } }));
vi.mock('./native-adapter', () => ({
    nativePhotoBackupAvailable: () => true,
    nativePhotoBackupPolicy: () => Promise.resolve(true),
    requestNativePhotoBackupAccess: mocks.requestAccess,
    nativePhotoBackupFolderPicking: () => true, pickNativePhotoBackupFolder: mocks.pickFolder,
    listNativePhotoBackupAssets: mocks.list, materializeNativePhotoBackupAsset: mocks.materialize, releaseNativePhotoBackupAsset: mocks.release,
}));
vi.mock('../encryption', () => ({ requireEncryptionPassword: mocks.unlock }));
// The real helper is what production runs; this mirrors its contract so the
// prompt spy sees exactly the calls the controller makes.
vi.mock('../modals/encryption-password', () => ({
    openEncryptionPasswordModal: mocks.prompt,
    callWithPasswordRetry: async (call: () => Promise<OperationResult>): Promise<OperationResult> => {
        let result = await call();
        if (!result.ok && result.error.code === 'encryption_password_required') {
            if (!await mocks.prompt()) return { ok: false, error: { code: 'canceled', message: 'Encryption password entry was canceled' } };
            result = await call();
        }
        return result;
    },
}));
vi.mock('../errors', () => ({ humanizeBackendError: (error: unknown) => String((error as { message?: string })?.message ?? '') }));

import { activatePhotoBackup, choosePhotoBackupFolder, pausePhotoBackupNow, photoBackupAccessNote, photoBackupError, photoBackupState, refreshPhotoBackup, resumePhotoBackupNow, retryPhotoBackupNow, startPhotoBackup } from './controller';
import { activeTransfers } from '../../ui/notifications/notif-store';
import { sidebarState } from '../../ui/sidebar/sidebar-store';

const flush = async () => { for (let i = 0; i < 8; i += 1) await new Promise((resolve) => setTimeout(resolve, 0)); };
const state = (): PhotoBackupState => ({ settings: { enabled: true, photos: true, videos: true, wifiOnly: false, encrypt: true }, sources: [{ id: 'tree:external_primary:DCIM/', kind: 'device-folder', root: 'external_primary:DCIM/', name: 'DCIM', enabled: true, addedAt: 0 }], status: { phase: 'idle', pending: 0, uploading: 0, complete: 0, failed: 0, paused: 0, bytesDone: 0, bytesTotal: 0, currentFile: '', currentFileBytesDone: 0, currentFileBytesTotal: 0, currentFilePercent: 0, message: '' }, capabilities: { wifiOnly: { supported: true, label: '', detail: '' }, access: { status: 'granted', detail: '' }, }, platform: 'android', destination: { id: '1', title: 'Personal', kind: 'personal' }, manualPaused: false, encryptionRequired: false });

describe('photo backup controller scheduler', () => {
    let stop = () => {};
    beforeEach(() => {
        stop(); mocks.events.clear();
        for (const mock of [mocks.enqueue, mocks.run, mocks.pause, mocks.resume, mocks.retry, mocks.policy, mocks.unlock, mocks.prompt, mocks.list, mocks.materialize, mocks.release, mocks.getState]) mock.mockReset();
        for (const control of [mocks.run, mocks.pause, mocks.resume, mocks.retry]) control.mockResolvedValue(ok);
        // The host answers every access request with the grant it gave.
        mocks.requestAccess.mockReset().mockResolvedValue({ status: 'granted', detail: '' });
        mocks.unlock.mockResolvedValue(true); mocks.prompt.mockResolvedValue(true);
        mocks.state = state(); mocks.getState.mockImplementation(() => Promise.resolve(mocks.state));
        sidebarState.set({ personal: [], shared: [], pending: [], activeChannelId: null, virtualView: null });
        Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
        stop = activatePhotoBackup();
    });
    afterEach(() => { stop(); });

    it('automatically drains multiple bounded pages once', async () => {
        mocks.list.mockResolvedValueOnce({ assets: [{ id: '1', version: '1', name: 'a', mediaType: 'photo', modifiedAt: 1, createdAt: 0, size: 1 }], nextCursor: 'next' }).mockResolvedValueOnce({ assets: [{ id: '2', version: '1', name: 'b', mediaType: 'photo', modifiedAt: 2, createdAt: 0, size: 1 }], nextCursor: '' });
        await startPhotoBackup(); await flush();
        expect(mocks.enqueue).toHaveBeenCalledTimes(2);
        mocks.events.get('photo-backup:state')?.({}); await flush();
        expect(mocks.enqueue).toHaveBeenCalledTimes(2);
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
        mocks.pause.mockRejectedValueOnce(new Error('offline'));
        await expect(pausePhotoBackupNow()).resolves.toBeUndefined();
        expect(get(photoBackupError)).toBe('Could not pause photo backup. Try again.');
    });

    it("repeats the backend's own reason when it declines to start", async () => {
        stop(); mocks.run.mockResolvedValue(waitingForWiFi);
        await startPhotoBackup();
        expect(get(photoBackupError)).toBe('Waiting for Wi-Fi.');
        expect(mocks.prompt).not.toHaveBeenCalled();
    });

    it('unlocks and retries an explicit backup once when the vault is locked', async () => {
        stop(); mocks.run.mockReset().mockResolvedValueOnce(locked).mockResolvedValueOnce(ok);
        await startPhotoBackup();
        expect(mocks.prompt).toHaveBeenCalledOnce();
        expect(mocks.run).toHaveBeenCalledTimes(2);
        expect(get(photoBackupError)).toBe('');
    });

    it('does not start an explicitly locked backup when unlocking is cancelled', async () => {
        stop(); mocks.run.mockClear();
        mocks.state = { ...state(), encryptionRequired: true };
        mocks.prompt.mockResolvedValueOnce(false);
        await refreshPhotoBackup();
        await startPhotoBackup();
        expect(mocks.prompt).toHaveBeenCalledOnce();
        expect(mocks.run).not.toHaveBeenCalled();
        expect(get(photoBackupError)).toBe('');
    });

    it('does not prompt from the automatic scheduler when encryption is locked', async () => {
        await flush(); mocks.run.mockClear();
        mocks.state = { ...state(), encryptionRequired: true };
        mocks.events.get('android:PhotoBackupMediaChanged')?.({});
        await flush();
        expect(mocks.unlock).not.toHaveBeenCalled();
        expect(mocks.prompt).not.toHaveBeenCalled();
        expect(mocks.run).not.toHaveBeenCalled();
        expect(get(photoBackupError)).toBe('Unlock encryption to continue photo backup.');
    });

    it('does not prompt when the backend reports a locked vault to the automatic scheduler', async () => {
        await flush(); mocks.list.mockClear();
        mocks.run.mockResolvedValue(locked);
        mocks.events.get('android:PhotoBackupMediaChanged')?.({});
        await flush();
        expect(mocks.prompt).not.toHaveBeenCalled();
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
