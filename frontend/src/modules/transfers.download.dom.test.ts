import { beforeEach, describe, expect, it, vi } from 'vitest';

const bindings = vi.hoisted(() => ({
    DownloadFile: vi.fn(),
    DownloadFolder: vi.fn(),
    SelectFiles: vi.fn(async () => []),
}));
const passwordModal = vi.hoisted(() => vi.fn(async () => false));
const transferEvents = vi.hoisted(() => ({
    push: vi.fn(),
    progress: vi.fn(),
    rename: vi.fn(),
    done: vi.fn(),
}));

vi.mock('../../wailsjs/go/main/App', () => bindings);
vi.mock('./notif-bell', () => ({
    pushTransferStart: transferEvents.push,
    updateTransferProgress: transferEvents.progress,
    updateTransferName: transferEvents.rename,
    markTransferDone: transferEvents.done,
}));
vi.mock('./notifications', () => ({ notify: vi.fn() }));
vi.mock('./encryption', () => ({ loadEncryptionStatus: vi.fn(async () => undefined) }));
vi.mock('./modals/upload-options', () => ({ openUploadOptionsModal: vi.fn() }));
vi.mock('./modals/import-options', () => ({ openImportOptionsModal: vi.fn() }));
vi.mock('./modals/encryption-setup', () => ({ openEncryptionSetupModal: vi.fn() }));
vi.mock('./modals/encryption-password', () => ({ openEncryptionPasswordModal: passwordModal }));

type Deferred<T> = {
    promise: Promise<T>;
    resolve: (value: T) => void;
};

type DownloadBindingResult = {
    status: string;
    message: string;
    saved_path: string;
};

function deferred<T>(): Deferred<T> {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((done) => { resolve = done; });
    return { promise, resolve };
}

async function settle(): Promise<void> {
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
}

async function loadModule() {
    vi.resetModules();
    const listeners = new Map<string, (payload: unknown) => void>();
    window.runtime = {
        EventsOn: vi.fn((name: string, callback: (payload: unknown) => void) => {
            listeners.set(name, callback);
        }),
    };
    const { idleTransferActivity, state } = await import('../state');
    state.downloadQueue = [];
    state.activeDownloadId = null;
    state.transferActivity = idleTransferActivity;
    state.cancelingDownload = false;
    const mod = await import('./transfers');
    mod.setupDownloadProgress();
    return { mod, state, listeners };
}

beforeEach(() => {
    vi.clearAllMocks();
    bindings.DownloadFile.mockResolvedValue({ status: 'success', message: 'done', saved_path: '/tmp/file' });
    bindings.DownloadFolder.mockResolvedValue({ status: 'success', message: 'done', saved_path: '/tmp/folder' });
    passwordModal.mockResolvedValue(false);
});

describe('folder download queue', () => {
    it('finalizes the notification before removing a completed folder job', async () => {
        const { mod, state } = await loadModule();
        const queueLengthAtNotificationFinalization: number[] = [];
        transferEvents.done.mockImplementation(() => {
            queueLengthAtNotificationFinalization.push(state.downloadQueue.length);
        });

        mod.enqueueFolderDownload('d:screenshots', 'Screenshots');
        await vi.waitFor(() => expect(bindings.DownloadFolder).toHaveBeenCalledWith('d:screenshots'));

        expect(bindings.DownloadFile).not.toHaveBeenCalled();
        await vi.waitFor(() => expect(transferEvents.done).toHaveBeenCalledWith({
            id: 'folder:d:screenshots',
            direction: 'down',
            status: 'done',
        }));
        expect(queueLengthAtNotificationFinalization).toEqual([1]);
        expect(state.downloadQueue).toEqual([]);
        expect(state.activeDownloadId).toBeNull();
        expect(state.transferActivity).toEqual({ upload: false, download: false });
    });

    it('serializes mixed file and folder downloads and starts the next job', async () => {
        const first = deferred<DownloadBindingResult>();
        const second = deferred<DownloadBindingResult>();
        bindings.DownloadFile.mockReturnValueOnce(first.promise);
        bindings.DownloadFolder.mockReturnValueOnce(second.promise);
        const { mod, state } = await loadModule();

        mod.enqueueDownload(42, 'first.txt', 10);
        mod.enqueueFolderDownload('d:next', 'Next');
        await settle();

        expect(bindings.DownloadFile).toHaveBeenCalledWith(42, 42);
        expect(bindings.DownloadFolder).not.toHaveBeenCalled();

        first.resolve({ status: 'success', message: 'done', saved_path: '/tmp/first.txt' });
        await vi.waitFor(() => expect(bindings.DownloadFolder).toHaveBeenCalledWith('d:next'));
        expect(state.downloadQueue).toEqual([
            expect.objectContaining({ key: 'folder:d:next', state: 'downloading' }),
        ]);

        second.resolve({ status: 'success', message: 'done', saved_path: '/tmp/next' });
        await vi.waitFor(() => expect(state.downloadQueue).toEqual([]));
        expect(state.transferActivity).toEqual({ upload: false, download: false });
    });

    it('allows a completed download to be enqueued again without retaining history in the scheduler', async () => {
        const { mod, state } = await loadModule();

        for (let attempt = 1; attempt <= 3; attempt += 1) {
            mod.enqueueDownload(42, 'again.txt', 10);
            await vi.waitFor(() => expect(bindings.DownloadFile).toHaveBeenCalledTimes(attempt));
            await vi.waitFor(() => expect(state.downloadQueue).toEqual([]));
        }

        expect(transferEvents.done).toHaveBeenCalledTimes(3);
    });

    it('updates exact aggregate byte and file progress without regressing', async () => {
        const pending = deferred<DownloadBindingResult>();
        bindings.DownloadFolder.mockReturnValueOnce(pending.promise);
        const { mod, state, listeners } = await loadModule();

        mod.enqueueFolderDownload('d:project', 'Project');
        await settle();
        const progress = listeners.get('folder_download_progress');
        expect(progress).toBeTypeOf('function');

        progress?.({
            folder_id: 'd:project',
            current_file: 'Data/a.bin',
            files_completed: 2,
            files_total: 5,
            bytes_completed: 60,
            bytes_total: 100,
            percent: 60,
        });
        progress?.({
            folder_id: 'd:project',
            current_file: 'Data/a.bin',
            files_completed: 1,
            files_total: 5,
            bytes_completed: 10,
            bytes_total: 100,
            percent: 10,
        });

        expect(state.downloadQueue[0]).toMatchObject({
            progress: 60,
            bytesCompleted: 60,
            bytesTotal: 100,
            filesCompleted: 2,
            filesTotal: 5,
        });
        expect(transferEvents.progress).toHaveBeenLastCalledWith({
            id: 'folder:d:project',
            direction: 'down',
            progress: 60,
            bytes: 60,
            total: 100,
            itemsDone: 2,
            itemsTotal: 5,
        });

        pending.resolve({ status: 'success', message: 'done', saved_path: '/tmp/Project' });
        await vi.waitFor(() => expect(state.downloadQueue).toEqual([]));
    });

    it('prompts once and retries the same folder after encryption unlock', async () => {
        bindings.DownloadFolder
            .mockResolvedValueOnce({ status: 'error', message: 'encryption password required' })
            .mockResolvedValueOnce({ status: 'success', message: 'done', saved_path: '/tmp/Locked' });
        passwordModal.mockResolvedValueOnce(true);
        const { mod } = await loadModule();

        mod.enqueueFolderDownload('d:locked', 'Locked');
        await vi.waitFor(() => expect(bindings.DownloadFolder).toHaveBeenCalledTimes(2));

        expect(passwordModal).toHaveBeenCalledOnce();
        expect(bindings.DownloadFolder).toHaveBeenNthCalledWith(1, 'd:locked');
        expect(bindings.DownloadFolder).toHaveBeenNthCalledWith(2, 'd:locked');
    });

    it('does not retry when encryption unlock is canceled', async () => {
        bindings.DownloadFolder.mockResolvedValueOnce({
            status: 'error',
            message: 'encryption password required',
            saved_path: '',
        });
        const { mod, state } = await loadModule();
        passwordModal.mockResolvedValueOnce(false);
        mod.enqueueFolderDownload('d:locked', 'Locked');
        await vi.waitFor(() => expect(transferEvents.done).toHaveBeenCalledWith({
            id: 'folder:d:locked',
            direction: 'down',
            status: 'failed',
        }));

        expect(passwordModal).toHaveBeenCalledOnce();
        expect(bindings.DownloadFolder).toHaveBeenCalledOnce();
        expect(state.downloadQueue).toEqual([]);
    });

    it('keeps an overlapping upload active after a download completes', async () => {
        const { mod, state } = await loadModule();
        state.transferActivity = { upload: true, download: false };

        mod.enqueueFolderDownload('d:project', 'Project');

        await vi.waitFor(() => expect(state.downloadQueue).toEqual([]));
        expect(state.transferActivity).toEqual({ upload: true, download: false });
    });

    it('marks a canceled folder and continues with the next queued file', async () => {
        const first = deferred<DownloadBindingResult>();
        bindings.DownloadFolder.mockReturnValueOnce(first.promise);
        const { mod, state } = await loadModule();

        mod.enqueueFolderDownload('d:project', 'Project');
        mod.enqueueDownload(77, 'next.txt', 4);
        await settle();
        state.cancelingDownload = true;
        first.resolve({ status: 'error', message: 'context canceled', saved_path: '' });

        await vi.waitFor(() => expect(bindings.DownloadFile).toHaveBeenCalledWith(77, 77));
        await vi.waitFor(() => expect(state.downloadQueue).toEqual([]));
        expect(transferEvents.done).toHaveBeenCalledWith({
            id: 'folder:d:project',
            direction: 'down',
            status: 'canceled',
        });
    });
});