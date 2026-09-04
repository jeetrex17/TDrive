import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { idleTransferActivity, state } from '../state';

const mocks = vi.hoisted(() => ({
    markTransferDone: vi.fn(),
    notify: vi.fn(),
    openImportOptionsModal: vi.fn(),
    pushTransferStart: vi.fn(),
    updateTransferName: vi.fn(),
    updateTransferProgress: vi.fn(),
}));

vi.mock('../../wailsjs/go/main/App', () => ({
    DownloadFile: vi.fn(),
    SelectFiles: vi.fn(),
}));
vi.mock('./notifications', () => ({ notify: mocks.notify }));
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

import { importFolderWithParentID, setupUploadProgress } from './transfers';

type RuntimeHandler = (...args: unknown[]) => void;

interface TestNotice {
    level?: string;
    title?: string;
    body?: string;
}

const handlers = new Map<string, RuntimeHandler>();

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
    const app = {
        SelectFolder: vi.fn().mockResolvedValue('/tmp/empty-folder'),
        PlanImport: vi.fn().mockResolvedValue({
            files: 0,
            folders: 1,
            archives: 0,
            limitExceeded: false,
        }),
        ImportPaths: vi.fn(async () => {
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
        }),
    };
    Object.defineProperty(window, 'go', {
        configurable: true,
        value: { main: { App: app } },
    });
    Object.defineProperty(window, 'refreshFiles', {
        configurable: true,
        value: vi.fn(),
    });
    Object.defineProperty(window, 'runtime', {
        configurable: true,
        value: {
            EventsOn: vi.fn((name: string, handler: RuntimeHandler) => {
                handlers.set(name, handler);
            }),
        },
    });
    setupUploadProgress();
});

afterEach(() => {
    state.activeChannel = null;
    state.transferActivity = idleTransferActivity;
    state.cancelingUpload = false;
    state.importBatch = null;
    Reflect.deleteProperty(window, 'go');
    Reflect.deleteProperty(window, 'runtime');
    Reflect.deleteProperty(window, 'refreshFiles');
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
