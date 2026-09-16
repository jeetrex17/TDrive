import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
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
    markTransferDone: mocks.markTransferDone,
    pushTransferStart: mocks.pushTransferStart,
    updateTransferName: mocks.updateTransferName,
    updateTransferProgress: mocks.updateTransferProgress,
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
