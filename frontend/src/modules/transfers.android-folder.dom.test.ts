import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { idleTransferActivity, state } from '../state';

const mocks = vi.hoisted(() => ({
    markTransferDone: vi.fn(),
    notify: vi.fn(),
    openImportOptionsModal: vi.fn(),
    pushTransferStart: vi.fn(),
    updateTransferName: vi.fn(),
    updateTransferProgress: vi.fn(),
    refreshFiles: vi.fn(),
}));

const eventListeners = vi.hoisted(() => new Map<string, (...args: unknown[]) => void>());
const eventsOn = vi.hoisted(() => vi.fn((eventName: string, callback: (event: { name: string; data: unknown[] }) => void) => {
    eventListeners.set(eventName, (...args: unknown[]) => callback({ name: eventName, data: args }));
    return () => eventListeners.delete(eventName);
}));
const app = vi.hoisted(() => ({
    CreateFolder: vi.fn(),
    DownloadFile: vi.fn(),
    ImportPaths: vi.fn(),
    PlanImport: vi.fn(),
    SelectFiles: vi.fn(),
    SelectFolder: vi.fn(),
    UploadToDriveFS: vi.fn(),
}));

vi.mock('../../bindings/TDrive/app', () => app);
vi.mock('@wailsio/runtime', () => ({ Events: { On: eventsOn } }));
vi.mock('./notifications', () => ({ notify: mocks.notify }));
vi.mock('./app-actions', () => ({ appActions: () => ({ refreshFiles: mocks.refreshFiles }) }));
vi.mock('./notif-bell', () => ({
    markTransferDone: mocks.markTransferDone,
    pushTransferStart: mocks.pushTransferStart,
    updateTransferName: mocks.updateTransferName,
    updateTransferProgress: mocks.updateTransferProgress,
    wasUploadCanceled: () => false,
}));
vi.mock('./encryption', () => ({ loadEncryptionStatus: vi.fn() }));
vi.mock('./modals/import-options', () => ({ openImportOptionsModal: mocks.openImportOptionsModal }));
vi.mock('./modals/upload-options', () => ({ openUploadOptionsModal: vi.fn() }));
vi.mock('./modals/encryption-setup', () => ({ openEncryptionSetupModal: vi.fn() }));
vi.mock('./modals/encryption-password', () => ({ openEncryptionPasswordModal: vi.fn() }));
vi.mock('../ui/chrome/UploadMenu.svelte', () => ({ default: {} }));
vi.mock('../ui/mount', () => ({ mountSvelte: vi.fn() }));

import { activateTransferSurfaces, importFolderWithParentID } from './transfers';

interface BridgeWindow {
    wails?: {
        pickFolder?: (id: string) => void;
        materializeFiles?: (id: string, idsJson: string) => void;
        releaseFiles?: (id: string, idsJson: string) => void;
    };
    _wailsAndroidCallback?: ((id: string, result: string | null, error: string | null) => void) | undefined;
    _tdriveBridgeCallbacks?: Record<string, unknown>;
}

const host = window as unknown as BridgeWindow;

let manifestJson = '';
let unreadable = new Set<string>();
let materialized: string[][] = [];
let released: string[][] = [];
let createdFolders: { id: string; name: string; parentId: string }[] = [];

/**
 * Stands in for the Android bridge. It answers on the spot, which the real one
 * never does, but the callback is registered before the bridge is called so
 * the orchestration cannot tell the difference.
 */
function installBridge(): void {
    const answer = (id: string, result: string) => host._wailsAndroidCallback?.(id, result, null);
    host.wails = {
        pickFolder: (id) => answer(id, manifestJson),
        materializeFiles: (id, idsJson) => {
            const ids = JSON.parse(idsJson) as string[];
            materialized.push(ids);
            const paths: Record<string, string> = {};
            for (const fileId of ids) {
                if (!unreadable.has(fileId)) paths[fileId] = `/cache/${fileId}`;
            }
            answer(id, JSON.stringify({ paths }));
        },
        releaseFiles: (id, idsJson) => {
            released.push(JSON.parse(idsJson) as string[]);
            answer(id, '');
        },
    };
}

function manifestOf(root: string, rels: string[], size = 100): string {
    return JSON.stringify({
        root,
        files: rels.map((rel, index) => ({ id: `doc:${index}`, rel, size })),
    });
}

let deactivateTransfers = () => {};

beforeEach(() => {
    vi.clearAllMocks();
    eventListeners.clear();
    state.activeChannel = { id: 1, title: 'Test drive', kind: 'shared' };
    state.transferActivity = idleTransferActivity;
    state.cancelingUpload = false;
    state.importBatch = null;
    state.uploadBatch = null;
    state.uploadTransfers = new Map();

    manifestJson = manifestOf('Holiday', ['a.jpg']);
    unreadable = new Set();
    materialized = [];
    released = [];
    createdFolders = [];

    mocks.openImportOptionsModal.mockResolvedValue({ encrypt: false, extract: false });
    app.CreateFolder.mockImplementation(async (name: string, parentId: string) => {
        const folder = { id: `f${createdFolders.length + 1}`, name, parentId };
        createdFolders.push(folder);
        return { id: folder.id, name, parent_id: parentId };
    });
    app.UploadToDriveFS.mockImplementation(async (paths: string[]) => ({
        result: { ok: true },
        files: paths.map((path) => ({ name: path })),
    }));

    installBridge();
    deactivateTransfers = activateTransferSurfaces();
});

afterEach(() => {
    state.activeChannel = null;
    state.transferActivity = idleTransferActivity;
    state.cancelingUpload = false;
    state.importBatch = null;
    delete host.wails;
    host._wailsAndroidCallback = undefined;
    delete host._tdriveBridgeCallbacks;
    deactivateTransfers();
});

describe('uploading an Android folder a window at a time', () => {
    it('copies, uploads and releases four files at a time', async () => {
        manifestJson = manifestOf('Holiday', ['a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i']);

        await importFolderWithParentID('root');

        expect(materialized).toEqual([
            ['doc:0', 'doc:1', 'doc:2', 'doc:3'],
            ['doc:4', 'doc:5', 'doc:6', 'doc:7'],
            ['doc:8'],
        ]);
        // Nothing stays in the cache behind the window that is uploading.
        expect(released).toEqual(materialized);
        expect(app.UploadToDriveFS).toHaveBeenCalledTimes(3);
    });

    it('creates the folder tree parents first and uploads each file into its own', async () => {
        manifestJson = manifestOf('Holiday', ['b/deep/x.jpg', 'a/y.jpg', 'top.jpg']);

        await importFolderWithParentID('root');

        expect(createdFolders).toEqual([
            { id: 'f1', name: 'Holiday', parentId: 'root' },
            { id: 'f2', name: 'b', parentId: 'f1' },
            { id: 'f3', name: 'a', parentId: 'f1' },
            { id: 'f4', name: 'deep', parentId: 'f2' },
        ]);
        const [paths, parentIDs] = app.UploadToDriveFS.mock.calls[0];
        expect(paths).toEqual(['/cache/doc:0', '/cache/doc:1', '/cache/doc:2']);
        expect(parentIDs).toEqual(['f4', 'f3', 'f1']);
    });

    it('releases the window even when the upload throws', async () => {
        manifestJson = manifestOf('Holiday', ['a', 'b']);
        app.UploadToDriveFS.mockRejectedValue(new Error('backend went away'));

        await importFolderWithParentID('root');

        expect(released).toEqual([['doc:0', 'doc:1']]);
        expect(mocks.markTransferDone).toHaveBeenCalledWith({ id: 'import', direction: 'up', status: 'failed' });
    });

    it('releases the window it was cancelled in and starts no more', async () => {
        manifestJson = manifestOf('Holiday', ['a', 'b', 'c', 'd', 'e']);
        app.UploadToDriveFS.mockResolvedValue({
            result: { ok: false, error: { code: 'canceled', message: 'canceled' } },
            files: [],
        });

        await importFolderWithParentID('root');

        expect(app.UploadToDriveFS).toHaveBeenCalledTimes(1);
        expect(released).toEqual([['doc:0', 'doc:1', 'doc:2', 'doc:3']]);
        expect(mocks.markTransferDone).toHaveBeenCalledWith({ id: 'import', direction: 'up', status: 'canceled' });
        // Backing out is not a failure to report.
        expect(mocks.notify).not.toHaveBeenCalled();
    });

    it('uploads the rest of a window when one file will not copy', async () => {
        manifestJson = manifestOf('Holiday', ['a', 'b', 'c']);
        unreadable = new Set(['doc:1']);

        await importFolderWithParentID('root');

        const [paths] = app.UploadToDriveFS.mock.calls[0];
        expect(paths).toEqual(['/cache/doc:0', '/cache/doc:2']);
        // The id that never became a file is still released: a half-done copy
        // would otherwise sit in the cache forever.
        expect(released).toEqual([['doc:0', 'doc:1', 'doc:2']]);
        expect(mocks.markTransferDone).toHaveBeenCalledWith({ id: 'import', direction: 'up', status: 'failed' });
        expect(mocks.updateTransferName).toHaveBeenLastCalledWith({
            id: 'import',
            direction: 'up',
            name: 'Imported 2 files · 1 failed',
        });
    });

    it('releases a window whose files all failed to copy', async () => {
        manifestJson = manifestOf('Holiday', ['a', 'b']);
        unreadable = new Set(['doc:0', 'doc:1']);

        await importFolderWithParentID('root');

        expect(app.UploadToDriveFS).not.toHaveBeenCalled();
        expect(released).toEqual([['doc:0', 'doc:1']]);
    });

    it('reports progress against the manifest, not the window', async () => {
        manifestJson = JSON.stringify({
            root: 'Holiday',
            files: [
                { id: 'doc:0', rel: 'a.jpg', size: 400 },
                { id: 'doc:1', rel: 'b.jpg', size: 600 },
            ],
        });

        await importFolderWithParentID('root');

        expect(mocks.updateTransferProgress).toHaveBeenLastCalledWith({
            id: 'import',
            direction: 'up',
            progress: 100,
            bytes: 1000,
            total: 1000,
            itemsDone: 2,
            itemsTotal: 2,
        });
        expect(mocks.markTransferDone).toHaveBeenCalledWith({ id: 'import', direction: 'up', status: 'done' });
    });

    it('shows the walk is running before the manifest arrives', async () => {
        await importFolderWithParentID('root');

        expect(mocks.pushTransferStart).toHaveBeenCalledWith({
            id: 'import',
            direction: 'up',
            name: 'Reading folder…',
            total: 0,
        });
    });

    it('does nothing at all when the picker is dismissed', async () => {
        manifestJson = '';

        await importFolderWithParentID('root');

        expect(app.CreateFolder).not.toHaveBeenCalled();
        expect(app.UploadToDriveFS).not.toHaveBeenCalled();
        expect(mocks.openImportOptionsModal).not.toHaveBeenCalled();
        expect(mocks.notify).not.toHaveBeenCalled();
        expect(mocks.markTransferDone).toHaveBeenCalledWith({ id: 'import', direction: 'up', status: 'canceled' });
    });

    it('uploads nothing when the import dialog is dismissed', async () => {
        mocks.openImportOptionsModal.mockResolvedValue(null);

        await importFolderWithParentID('root');

        expect(app.CreateFolder).not.toHaveBeenCalled();
        expect(materialized).toEqual([]);
        expect(mocks.markTransferDone).toHaveBeenCalledWith({ id: 'import', direction: 'up', status: 'canceled' });
    });

    it('stops when the tree cannot be created, rather than uploading into the wrong place', async () => {
        manifestJson = manifestOf('Holiday', ['sub/a.jpg']);
        app.CreateFolder.mockRejectedValue(new Error('folder projection failed'));

        await importFolderWithParentID('root');

        expect(materialized).toEqual([]);
        expect(app.UploadToDriveFS).not.toHaveBeenCalled();
        expect(mocks.markTransferDone).toHaveBeenCalledWith({ id: 'import', direction: 'up', status: 'failed' });
    });

    it('leaves the desktop picker alone', async () => {
        delete host.wails;
        app.SelectFolder.mockResolvedValue('');

        await importFolderWithParentID('root');

        expect(app.SelectFolder).toHaveBeenCalled();
        expect(materialized).toEqual([]);
    });
});
