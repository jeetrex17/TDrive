import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { TransferItem } from '../ui/notifications/notif-store';
import { idleTransferActivity, state } from '../state';

const mocks = vi.hoisted(() => ({
    dismissNotification: vi.fn(),
    markTransferDone: vi.fn(),
    notify: vi.fn(),
    openImportOptionsModal: vi.fn(),
    pushTransferStart: vi.fn(),
    updateTransferName: vi.fn(),
    updateTransferProgress: vi.fn(),
    refreshFiles: vi.fn(),
    wasUploadCanceled: vi.fn(() => false),
}));

// Go always emits an event's payload as a JSON array of its original args
// (see RuntimeEventMap in api/runtime.ts); mimic that instead of the removed
// window.runtime.EventsOn bridge.
const eventListeners = vi.hoisted(() => new Map<string, (...args: unknown[]) => void>());
const eventsOn = vi.hoisted(() => vi.fn((eventName: string, callback: (event: { name: string; data: unknown[] }) => void) => {
    eventListeners.set(eventName, (...args: unknown[]) => callback({ name: eventName, data: args }));
    return () => eventListeners.delete(eventName);
}));
const app = vi.hoisted(() => ({
    DownloadFile: vi.fn(),
    ImportPaths: vi.fn(),
    PlanImport: vi.fn(),
    SelectFiles: vi.fn(),
    SelectFolder: vi.fn(),
    UploadToDriveFS: vi.fn(),
}));

vi.mock('../../bindings/TDrive/app', () => app);
vi.mock('@wailsio/runtime', () => ({ Events: { On: eventsOn } }));
vi.mock('./notifications', () => ({ notify: mocks.notify, dismissNotification: mocks.dismissNotification }));
vi.mock('./app-actions', () => ({ appActions: () => ({ refreshFiles: mocks.refreshFiles }) }));
vi.mock('./notif-bell', () => ({
    setTransferNote: vi.fn(),
    markTransferDone: mocks.markTransferDone,
    pushTransferStart: mocks.pushTransferStart,
    updateTransferName: mocks.updateTransferName,
    updateTransferProgress: mocks.updateTransferProgress,
    wasUploadCanceled: mocks.wasUploadCanceled,
}));
vi.mock('./encryption', () => ({ loadEncryptionStatus: vi.fn() }));
vi.mock('./modals/import-options', () => ({
    openImportOptionsModal: mocks.openImportOptionsModal,
}));
vi.mock('./modals/upload-options', () => ({ openUploadOptionsModal: vi.fn() }));
vi.mock('./modals/encryption-setup', () => ({ openEncryptionSetupModal: vi.fn() }));
vi.mock('./modals/encryption-password', () => ({ openEncryptionPasswordModal: vi.fn() }));
vi.mock('../ui/chrome/UploadMenu.svelte', () => ({ default: {} }));
vi.mock('../ui/mount', () => ({ mountSvelte: vi.fn() }));

import { activateTransferSurfaces, importFolderWithParentID, uploadWithParentID } from './transfers';

interface TestNotice {
    level?: string;
    title?: string;
    body?: string;
}

const handlers = eventListeners;
let deactivateTransfers = () => {};

beforeEach(() => {
    vi.clearAllMocks();
    handlers.clear();
    state.activeChannel = { id: 1, title: 'Test drive', kind: 'shared' };
    state.transferActivity = idleTransferActivity;
    state.cancelingUpload = false;
    state.importBatch = null;
    state.uploadBatch = null;
    state.uploadTransfers = new Map();
    mocks.wasUploadCanceled.mockReturnValue(false);

    mocks.openImportOptionsModal.mockResolvedValue({ encrypt: false, extract: false });
    app.SelectFolder.mockResolvedValue('/tmp/empty-folder');
    app.PlanImport.mockResolvedValue({
        files: 0,
        folders: 1,
        archives: 0,
        limitExceeded: false,
    });
    app.ImportPaths.mockImplementation(async () => {
        handlers.get('import_start')?.();
        handlers.get('import_complete')?.({
            status: 'failed',
            error: 'folder projection failed after Telegram accepted the folder',
            uploaded: 0,
            failed: 0,
            folders: 0,
            oversize: 0,
            ignored: 0,
            errorCount: 0,
            errors: [],
        });
        throw new Error('folder projection failed after Telegram accepted the folder');
    });

    deactivateTransfers = activateTransferSurfaces();
});

afterEach(() => {
    state.activeChannel = null;
    state.transferActivity = idleTransferActivity;
    state.cancelingUpload = false;
    state.importBatch = null;
    deactivateTransfers();
});

describe('aggregate import completion', () => {
    it('reports a fatal completion once even when ImportPaths subsequently rejects', async () => {
        await importFolderWithParentID('');

        expect(mocks.markTransferDone).toHaveBeenCalledWith({
            id: 'import',
            direction: 'up',
            status: 'failed',
        });
        expect(mocks.markTransferDone).toHaveBeenCalledTimes(1);
        expect(mocks.markTransferDone).not.toHaveBeenCalledWith(expect.objectContaining({ status: 'done' }));
        expect(mocks.pushTransferStart).not.toHaveBeenCalledWith(expect.objectContaining({ name: 'Import failed' }));
        expect(mocks.updateTransferName).toHaveBeenLastCalledWith({
            id: 'import',
            direction: 'up',
            name: 'Import failed',
        });

        const errorNotifications = mocks.notify.mock.calls
            .map(([notice]) => notice as TestNotice)
            .filter((notice) => notice?.level === 'error');
        expect(errorNotifications).toHaveLength(1);
        expect(errorNotifications[0].title).toBe('Import failed');
        expect(errorNotifications[0].body).toContain('folder projection failed');
    });

    it('keeps cancellation authoritative over a late fatal status', () => {
        handlers.get('import_start')?.();
        state.cancelingUpload = true;
        handlers.get('import_complete')?.({
            status: 'failed',
            error: 'request canceled while a write was settling',
            uploaded: 0,
            failed: 0,
            errorCount: 0,
            errors: [],
        });

        expect(mocks.markTransferDone).toHaveBeenCalledWith({
            id: 'import',
            direction: 'up',
            status: 'canceled',
        });
        expect(mocks.notify).not.toHaveBeenCalled();
    });

    it('preserves partial-import wording for non-fatal file failures', () => {
        handlers.get('import_start')?.();
        handlers.get('import_complete')?.({
            status: 'done',
            uploaded: 999,
            failed: 1,
            errorCount: 0,
            errors: [],
        });

        expect(mocks.updateTransferName).toHaveBeenLastCalledWith({
            id: 'import',
            direction: 'up',
            name: 'Imported 999 files · 1 failed',
        });
        expect(mocks.markTransferDone).toHaveBeenCalledWith({
            id: 'import',
            direction: 'up',
            status: 'failed',
        });
        expect(mocks.notify).toHaveBeenCalledWith(expect.objectContaining({
            level: 'error',
            title: 'Imported 999 files · 1 failed',
            body: expect.stringContaining('1 failed'),
        }));
    });
});

describe('native file drop', () => {
    it('accepts OS file drags at the page level and imports drops over the file list', async () => {
        document.body.innerHTML = '<div id="file-list"></div><div id="sidebar"></div>';
        const list = document.getElementById('file-list');
        let underPointer: Element | null = list;
        Object.defineProperty(document, 'elementFromPoint', {
            configurable: true,
            value: () => underPointer,
        });
        state.currentFolderId = 'folder-7';

        handlers.get('files_dropped')?.({ x: 40, y: 80, paths: ['/tmp/movie.mkv'] });
        await vi.waitFor(() => expect(app.PlanImport).toHaveBeenCalledWith(['/tmp/movie.mkv'], false, false));

        underPointer = document.getElementById('sidebar');
        handlers.get('files_dropped')?.({ x: 5, y: 5, paths: ['/tmp/ignored.txt'] });
        await new Promise((resolve) => setTimeout(resolve, 0));
        expect(app.PlanImport).toHaveBeenCalledTimes(1);

        Reflect.deleteProperty(document, 'elementFromPoint');
        state.currentFolderId = '';
    });
});

describe('the picker window', () => {
    it('refuses a second Upload while the host is still preparing the first', async () => {
        // A phone copies every picked document out of its content provider
        // before this resolves, which is seconds of an unchanged screen -- and
        // an unchanged screen is exactly when a reader taps Upload again.
        let releasePicker = (_paths: string[]) => {};
        app.SelectFiles.mockReturnValue(new Promise<string[]>((resolve) => {
            releasePicker = resolve;
        }));
        app.PlanImport.mockResolvedValue({ files: 1, folders: 0, archives: 0, limitExceeded: false });
        app.UploadToDriveFS.mockResolvedValue({ result: { ok: true }, files: [] });

        const first = uploadWithParentID('');
        await Promise.resolve();
        // Still inside the picker: the lock has to already be held.
        await uploadWithParentID('');
        expect(app.SelectFiles).toHaveBeenCalledTimes(1);
        expect(mocks.notify).toHaveBeenCalledWith(
            expect.objectContaining({ title: 'A transfer is already in progress' }),
        );

        // The folder picker sits behind the same lock, so it is refused too
        // rather than racing the selection that is already being prepared.
        await importFolderWithParentID('');
        expect(app.SelectFolder).not.toHaveBeenCalled();

        releasePicker(['/tmp/report.pdf']);
        await first;
        expect(app.UploadToDriveFS).toHaveBeenCalledTimes(1);

        // And the lock is released with it, so the next upload is not rejected.
        app.SelectFiles.mockResolvedValue(['/tmp/second.pdf']);
        await uploadWithParentID('');
        expect(app.SelectFiles).toHaveBeenCalledTimes(2);
    });

    it('asks for the plan and the encryption snapshot together, not one after the other', async () => {
        // The snapshot does not depend on the selection, so it has no business
        // sitting between the picker closing and the modal opening.
        state.activeChannel = { id: 1, title: 'Personal', kind: 'personal' };
        let planStarted = false;
        let encryptionSettled = false;
        const { loadEncryptionStatus } = await import('./encryption');
        vi.mocked(loadEncryptionStatus).mockImplementation(async () => {
            // Resolves only after the plan has been asked for; if the two were
            // serialized on the snapshot this would deadlock the flow.
            await vi.waitFor(() => expect(planStarted).toBe(true));
            encryptionSettled = true;
        });
        app.SelectFiles.mockResolvedValue(['/tmp/report.pdf']);
        app.PlanImport.mockImplementation(async () => {
            planStarted = true;
            return { files: 1, folders: 0, archives: 0, limitExceeded: false };
        });

        await uploadWithParentID('');

        expect(planStarted).toBe(true);
        expect(encryptionSettled).toBe(true);
        state.activeChannel = { id: 1, title: 'Test drive', kind: 'shared' };
    });
});

describe('upload retry', () => {
    it('takes its own toast down and refuses a second tap on the same failure', async () => {
        app.PlanImport.mockResolvedValue({ files: 1, folders: 0, archives: 0, limitExceeded: false });
        app.SelectFiles.mockResolvedValue(['/tmp/report.pdf']);
        app.UploadToDriveFS.mockResolvedValue({
            result: { ok: false, error: { code: 'io', message: 'connection reset' } },
            files: [],
        });

        await uploadWithParentID('');
        expect(app.UploadToDriveFS).toHaveBeenCalledTimes(1);

        const failure = mocks.notify.mock.calls
            .map(([notice]) => notice as TestNotice & { action?: { label: string; run: () => void } })
            .find((notice) => notice?.title === 'Upload failed');
        expect(failure?.action?.label).toBe('Retry');

        failure?.action?.run();
        failure?.action?.run();
        await vi.waitFor(() => expect(app.UploadToDriveFS).toHaveBeenCalledTimes(2));
        await new Promise((resolve) => setTimeout(resolve, 0));

        // A second batch over the first would cancel it on the backend, which
        // keeps one cancel handle per direction.
        expect(app.UploadToDriveFS).toHaveBeenCalledTimes(2);
        expect(mocks.dismissNotification).toHaveBeenCalledTimes(1);
        // The second tap is inert, not rebuffed: a double-tap on a button that
        // has already been taken should not answer back with a second toast.
        expect(mocks.notify).not.toHaveBeenCalledWith(
            expect.objectContaining({ title: 'A transfer is already in progress' }),
        );
    });
});

describe('stopping one upload from its own row', () => {
    it('ends that file as canceled and says nothing the user did not ask for', () => {
        mocks.wasUploadCanceled.mockReturnValue(true);
        handlers.get('upload_start')?.(1, 'holiday.mov', 1000, '');
        handlers.get('upload_error')?.(1, 'holiday.mov', 'context canceled');

        expect(mocks.markTransferDone).toHaveBeenCalledWith({ id: 1, direction: 'up', status: 'canceled' });
        expect(mocks.notify).not.toHaveBeenCalled();
    });

    it('still reports a real failure on a file nobody stopped', () => {
        handlers.get('upload_start')?.(2, 'notes.txt', 10, '');
        handlers.get('upload_error')?.(2, 'notes.txt', 'FLOOD_WAIT (420)');

        expect(mocks.markTransferDone).toHaveBeenCalledWith({ id: 2, direction: 'up', status: 'failed' });
        expect(mocks.notify).toHaveBeenCalledWith(expect.objectContaining({ level: 'error' }));
    });
});

describe('a multi-file upload reports as one transfer', () => {
    async function runBatch(paths: string[], run: () => void = () => {}): Promise<void> {
        app.SelectFiles.mockResolvedValue(paths);
        app.PlanImport.mockResolvedValue({ files: paths.length, folders: 0, archives: 0, limitExceeded: false });
        app.UploadToDriveFS.mockImplementation(async () => {
            run();
            return { result: { ok: true } };
        });
        await uploadWithParentID('');
    }

    it('opens one aggregate row for the batch and lists only the files in flight', async () => {
        await runBatch(Array.from({ length: 200 }, (_, index) => `/tmp/file-${index}.bin`), () => {
            for (let id = 0; id < 3; id++) handlers.get('upload_start')?.(id, `file-${id}.bin`, 1_000, '');
            handlers.get('upload_progress')?.(0, 50);
            handlers.get('upload_complete')?.(1, 'file-1.bin');
        });

        // One row for two hundred files. Two hundred rows would say nothing
        // about the batch and would push the rest of the bell past its cap.
        const started = mocks.pushTransferStart.mock.calls.map(([call]) => call);
        expect(started).toHaveLength(1);
        expect(started[0]).toMatchObject({ id: 'upload-batch', direction: 'up', name: 'Uploading 200 files' });

        const midFlight = mocks.updateTransferProgress.mock.calls
            .map(([call]) => call)
            .filter((call) => call.itemsActive === 2)
            .pop();
        expect(midFlight).toMatchObject({ id: 'upload-batch', itemsDone: 1, itemsTotal: 200 });
        expect(midFlight?.items?.map((item: TransferItem) => item.name)).toEqual(['file-0.bin', 'file-2.bin']);

        // Every file is accounted for once the call returns, whether or not its
        // own event arrived, and the row ends saying what happened.
        expect(mocks.updateTransferName).toHaveBeenLastCalledWith({
            id: 'upload-batch', direction: 'up', name: 'Uploaded 200 files',
        });
        expect(mocks.markTransferDone).toHaveBeenCalledWith({
            id: 'upload-batch', direction: 'up', status: 'done',
        });
    });

    it('folds the batch\'s failures into one summary rather than one toast per file', async () => {
        await runBatch(Array.from({ length: 50 }, (_, index) => `/tmp/file-${index}.bin`), () => {
            for (let id = 0; id < 50; id++) {
                handlers.get('upload_start')?.(id, `file-${id}.bin`, 10, '');
                handlers.get('upload_error')?.(id, `file-${id}.bin`, 'FLOOD_WAIT (420)');
            }
        });

        const errors = mocks.notify.mock.calls
            .map(([notice]) => notice as TestNotice)
            .filter((notice) => notice.level === 'error');
        expect(errors).toHaveLength(1);
        expect(errors[0].title).toBe("Couldn't upload 50 files");
        expect(mocks.markTransferDone).toHaveBeenCalledWith({
            id: 'upload-batch', direction: 'up', status: 'failed',
        });
    });

    it('leaves a single file its own row, where the file is the transfer', async () => {
        await runBatch(['/tmp/only.bin'], () => {
            handlers.get('upload_start')?.(0, 'only.bin', 1_000, '');
            handlers.get('upload_complete')?.(0, 'only.bin');
        });

        expect(mocks.pushTransferStart).toHaveBeenCalledWith(
            expect.objectContaining({ id: 0, direction: 'up', name: 'only.bin' }),
        );
        expect(mocks.pushTransferStart).not.toHaveBeenCalledWith(
            expect.objectContaining({ id: 'upload-batch' }),
        );
    });
});
