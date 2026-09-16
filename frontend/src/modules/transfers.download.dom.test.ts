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
const notifications = vi.hoisted(() => ({ notify: vi.fn(), dismissNotification: vi.fn() }));

// Go always emits an event's payload as a JSON array of its original args
// (see RuntimeEventMap in api/runtime.ts); mimic that instead of the removed
// window.runtime.EventsOn bridge.
const eventListeners = vi.hoisted(() => new Map<string, (...args: unknown[]) => void>());
const eventsOn = vi.hoisted(() => vi.fn((eventName: string, callback: (event: { name: string; data: unknown[] }) => void) => {
    eventListeners.set(eventName, (...args: unknown[]) => callback({ name: eventName, data: args }));
    return () => eventListeners.delete(eventName);
}));

vi.mock('../../bindings/TDrive/app', () => bindings);
vi.mock('@wailsio/runtime', () => ({ Events: { On: eventsOn } }));
vi.mock('./notif-bell', () => ({
    pushTransferStart: transferEvents.push,
    updateTransferProgress: transferEvents.progress,
    updateTransferName: transferEvents.rename,
    markTransferDone: transferEvents.done,
}));
vi.mock('./notifications', () => ({
    notify: notifications.notify,
    dismissNotification: notifications.dismissNotification,
}));
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
    result:
        | { ok: true }
        | { ok: false; error: { code: string; message: string } };
    saved_path: string;
};

function downloadSuccess(savedPath: string): DownloadBindingResult {
    return { result: { ok: true }, saved_path: savedPath };
}

function downloadFailure(code: string, message: string): DownloadBindingResult {
    return { result: { ok: false, error: { code, message } }, saved_path: '' };
}

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
    eventListeners.clear();
    const { idleTransferActivity, state } = await import('../state');
    state.downloadQueue = [];
    state.activeDownloadId = null;
    state.transferActivity = idleTransferActivity;
    state.cancelingDownload = false;
    const mod = await import('./transfers');
    mod.activateTransferSurfaces();
    return { mod, state, listeners: eventListeners };
}

beforeEach(() => {
    vi.clearAllMocks();
    bindings.DownloadFile.mockResolvedValue(downloadSuccess('/tmp/file'));
    bindings.DownloadFolder.mockResolvedValue(downloadSuccess('/tmp/folder'));
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

        first.resolve(downloadSuccess('/tmp/first.txt'));
        await vi.waitFor(() => expect(bindings.DownloadFolder).toHaveBeenCalledWith('d:next'));
        expect(state.downloadQueue).toEqual([
            expect.objectContaining({ key: 'folder:d:next', state: 'downloading' }),
        ]);

        second.resolve(downloadSuccess('/tmp/next'));
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

        pending.resolve(downloadSuccess('/tmp/Project'));
        await vi.waitFor(() => expect(state.downloadQueue).toEqual([]));
    });

    it('prompts once and retries the same folder after encryption unlock', async () => {
        bindings.DownloadFolder
            .mockResolvedValueOnce(downloadFailure('encryption_password_required', 'Unlock before downloading'))
            .mockResolvedValueOnce(downloadSuccess('/tmp/Locked'));
        passwordModal.mockResolvedValueOnce(true);
        const { mod } = await loadModule();

        mod.enqueueFolderDownload('d:locked', 'Locked');
        await vi.waitFor(() => expect(bindings.DownloadFolder).toHaveBeenCalledTimes(2));

        expect(passwordModal).toHaveBeenCalledOnce();
        expect(bindings.DownloadFolder).toHaveBeenNthCalledWith(1, 'd:locked');
        expect(bindings.DownloadFolder).toHaveBeenNthCalledWith(2, 'd:locked');
    });

    it('does not retry when encryption unlock is canceled', async () => {
        bindings.DownloadFolder.mockResolvedValueOnce(
            downloadFailure('encryption_password_required', 'Unlock before downloading'),
        );
        const { mod, state } = await loadModule();
        passwordModal.mockResolvedValueOnce(false);
        mod.enqueueFolderDownload('d:locked', 'Locked');
        await vi.waitFor(() => expect(transferEvents.done).toHaveBeenCalledWith({
            id: 'folder:d:locked',
            direction: 'down',
            status: 'canceled',
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

    it('surfaces the backend reason as an error toast when a folder download fails', async () => {
        bindings.DownloadFolder.mockResolvedValueOnce(
            downloadFailure('insufficient_storage', 'Storage unavailable'),
        );
        const { mod } = await loadModule();

        mod.enqueueFolderDownload('d:project', 'Project');
        await vi.waitFor(() => expect(notifications.notify).toHaveBeenCalledTimes(1));
        expect(notifications.notify).toHaveBeenCalledWith(expect.objectContaining({
            level: 'error',
            title: "Couldn't download Project",
            body: 'There is not enough free disk space to finish this action.',
        }));
    });

    it('shows an already-exists folder as a warning, not an error', async () => {
        bindings.DownloadFolder.mockResolvedValueOnce(
            downloadFailure('already_exists', 'Choose another destination'),
        );
        const { mod } = await loadModule();

        mod.enqueueFolderDownload('d:project', 'Project');
        await vi.waitFor(() => expect(notifications.notify).toHaveBeenCalledTimes(1));
        expect(notifications.notify).toHaveBeenCalledWith(expect.objectContaining({
            level: 'warning',
            title: 'Folder already exists',
        }));
    });

    it('does not toast an error when the user cancels the encryption prompt', async () => {
        bindings.DownloadFolder.mockResolvedValueOnce(
            downloadFailure('encryption_password_required', 'Vault is locked'),
        );
        passwordModal.mockResolvedValueOnce(false);
        const { mod } = await loadModule();

        mod.enqueueFolderDownload('d:locked', 'Locked');
        await vi.waitFor(() => expect(transferEvents.done).toHaveBeenCalledWith({
            id: 'folder:d:locked',
            direction: 'down',
            status: 'canceled',
        }));
        expect(notifications.notify).not.toHaveBeenCalled();
    });

    it('announces a successful folder download with its saved path', async () => {
        const { mod, state } = await loadModule();

        mod.enqueueFolderDownload('d:project', 'Project');
        await vi.waitFor(() => expect(state.downloadQueue).toEqual([]));
        expect(notifications.notify).toHaveBeenCalledWith(expect.objectContaining({
            level: 'success',
            title: 'Folder downloaded',
            body: 'Saved to /tmp/folder',
        }));
    });

    it('marks a canceled folder and continues with the next queued file', async () => {
        const first = deferred<DownloadBindingResult>();
        bindings.DownloadFolder.mockReturnValueOnce(first.promise);
        const { mod, state } = await loadModule();

        mod.enqueueFolderDownload('d:project', 'Project');
        mod.enqueueDownload(77, 'next.txt', 4);
        await settle();
        state.cancelingDownload = true;
        first.resolve(downloadFailure('canceled', 'context canceled'));

        await vi.waitFor(() => expect(bindings.DownloadFile).toHaveBeenCalledWith(77, 77));
        await vi.waitFor(() => expect(state.downloadQueue).toEqual([]));
        expect(transferEvents.done).toHaveBeenCalledWith({
            id: 'folder:d:project',
            direction: 'down',
            status: 'canceled',
        });
    });
});
describe('download retry', () => {
    function retryAction(): { label: string; run: () => void } {
        const failure = notifications.notify.mock.calls
            .map(([options]) => options as { level?: string; action?: { label: string; run: () => void } })
            .find((options) => options.level === 'error' && options.action);
        if (!failure?.action) throw new Error('no retry action was offered');
        return failure.action;
    }

    it('takes the retry down with it, so a sticky toast cannot start a second one', async () => {
        const second = deferred<DownloadBindingResult>();
        bindings.DownloadFile
            .mockResolvedValueOnce(downloadFailure('io', 'disk full'))
            .mockReturnValueOnce(second.promise);
        const { mod, state } = await loadModule();

        mod.enqueueDownload(42, 'plan.pdf', 10);
        await vi.waitFor(() => expect(state.downloadQueue).toEqual([]));

        const action = retryAction();
        action.run();
        await settle();
        // The same sticky toast is still on screen until the dismiss lands, so
        // the second tap is the one a thumb actually makes.
        action.run();
        await settle();

        expect(notifications.dismissNotification).toHaveBeenCalledTimes(1);
        expect(state.downloadQueue.filter((item) => item.key === 'file:42')).toHaveLength(1);
        expect(bindings.DownloadFile).toHaveBeenCalledTimes(2);

        second.resolve(downloadSuccess('/tmp/plan.pdf'));
        await vi.waitFor(() => expect(state.downloadQueue).toEqual([]));
    });

    it('re-queues the download behind a failed row, and only a download', async () => {
        const { mod, state } = await loadModule();

        const failedFile = {
            kind: 'transfer' as const,
            id: 'xfer:down:file:42',
            direction: 'down' as const,
            name: 'plan.pdf',
            progress: 0,
            total: 10,
            bytes: 0,
            speed: 0,
            status: 'failed' as const,
            startedAt: 0,
            finishedAt: 0,
        };
        const retry = mod.downloadRetryFor(failedFile);
        expect(retry).toBeTypeOf('function');
        retry?.();
        expect(state.downloadQueue[0]).toMatchObject({ key: 'file:42', name: 'plan.pdf', size: 10 });

        expect(mod.downloadRetryFor({ ...failedFile, status: 'done' })).toBeUndefined();
        expect(mod.downloadRetryFor({ ...failedFile, direction: 'up', id: 'xfer:up:2' })).toBeUndefined();
        expect(mod.downloadRetryFor({ ...failedFile, id: 'xfer:down:mystery' })).toBeUndefined();
    });

    it('re-queues a failed folder under the same key the row carries', async () => {
        const { mod, state } = await loadModule();

        const retry = mod.downloadRetryFor({
            kind: 'transfer',
            id: 'xfer:down:folder:d:project',
            direction: 'down',
            name: 'Project',
            progress: 0,
            total: 90,
            bytes: 0,
            speed: 0,
            status: 'failed',
            startedAt: 0,
            finishedAt: 0,
        });
        retry?.();
        expect(state.downloadQueue[0]).toMatchObject({ key: 'folder:d:project', kind: 'folder', name: 'Project' });
    });
});
