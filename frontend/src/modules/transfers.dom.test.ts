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
    const { state } = await import('../state');
    state.downloadQueue = [];
    state.activeDownloadId = null;
    state.activeTransfer = null;
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
    it('preserves a folder id and dispatches it to DownloadFolder', async () => {
        const { mod, state } = await loadModule();

        mod.enqueueFolderDownload('d:screenshots', 'Screenshots');
        await vi.waitFor(() => expect(bindings.DownloadFolder).toHaveBeenCalledWith('d:screenshots'));

        expect(bindings.DownloadFile).not.toHaveBeenCalled();
        expect(state.downloadQueue).toHaveLength(1);
        expect(state.downloadQueue[0]).toMatchObject({
            key: 'folder:d:screenshots',
            kind: 'folder',
            id: 'd:screenshots',
            state: 'done',
        });
    });

    it('serializes mixed file and folder downloads', async () => {
        const first = deferred<DownloadBindingResult>();
        bindings.DownloadFile.mockReturnValueOnce(first.promise);
        const { mod } = await loadModule();

        mod.enqueueDownload(42, 'first.txt', 10);
        mod.enqueueFolderDownload('d:next', 'Next');
        await settle();

        expect(bindings.DownloadFile).toHaveBeenCalledWith(42, 42);
        expect(bindings.DownloadFolder).not.toHaveBeenCalled();

        first.resolve({ status: 'success', message: 'done', saved_path: '/tmp/first.txt' });
        await vi.waitFor(() => expect(bindings.DownloadFolder).toHaveBeenCalledWith('d:next'));
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
        await settle();
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
        passwordModal.mockResolvedValueOnce(false);
        const { mod, state } = await loadModule();

        mod.enqueueFolderDownload('d:locked', 'Locked');
        await vi.waitFor(() => expect(state.downloadQueue[0]?.state).toBe('failed'));

        expect(passwordModal).toHaveBeenCalledOnce();
        expect(bindings.DownloadFolder).toHaveBeenCalledOnce();
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
        expect(state.downloadQueue.find((item) => item.key === 'folder:d:project')?.state).toBe('canceled');
        expect(state.downloadQueue.find((item) => item.key === 'file:77')?.state).toBe('done');
    });
});
