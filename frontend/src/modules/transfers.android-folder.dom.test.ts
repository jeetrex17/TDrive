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
    cancelSingleUpload: vi.fn(),
}));

const eventListeners = vi.hoisted(() => new Map<string, (...args: unknown[]) => void>());
const eventsOn = vi.hoisted(() => vi.fn((eventName: string, callback: (event: { name: string; data: unknown[] }) => void) => {
    eventListeners.set(eventName, (...args: unknown[]) => callback({ name: eventName, data: args }));
    return () => eventListeners.delete(eventName);
}));
const app = vi.hoisted(() => ({
    CreateFolderForDrive: vi.fn(),
    DownloadFile: vi.fn(),
    ImportPaths: vi.fn(),
    PlanImport: vi.fn(),
    SelectFiles: vi.fn(),
    SelectFolder: vi.fn(),
    RetryUploadToDriveFS: vi.fn(),
    ResumeUploads: vi.fn(),
    FlushEvents: vi.fn(async (token: string) => { eventListeners.get('events_flushed')?.(token); }),
}));

vi.mock('../../bindings/TDrive/internal/app/app', () => app);
vi.mock('@wailsio/runtime', () => ({ Events: { On: eventsOn } }));
vi.mock('./notifications', () => ({ notify: mocks.notify }));
vi.mock('./app-actions', () => ({ appActions: () => ({ refreshFiles: mocks.refreshFiles }) }));
vi.mock('./notif-bell', () => ({
    forgetResumableUpload: vi.fn(),
    markTransferDone: mocks.markTransferDone,
    pushTransferStart: mocks.pushTransferStart,
    updateTransferName: mocks.updateTransferName,
    updateTransferProgress: mocks.updateTransferProgress,
    wasUploadCanceled: () => false,
    cancelSingleUpload: mocks.cancelSingleUpload,
}));
vi.mock('./encryption', () => ({ loadEncryptionStatus: vi.fn() }));
vi.mock('./modals/import-options', () => ({ openImportOptionsModal: mocks.openImportOptionsModal, cancelImportOptions: vi.fn() }));
vi.mock('./modals/upload-options', () => ({ openUploadOptionsModal: vi.fn().mockResolvedValue({ encrypt: false }), cancelUploadOptions: vi.fn() }));
vi.mock('./modals/encryption-setup', () => ({ openEncryptionSetupModal: vi.fn() }));
vi.mock('./modals/encryption-password', () => ({ openEncryptionPasswordModal: vi.fn() }));
vi.mock('../ui/chrome/UploadMenu.svelte', () => ({ default: {} }));
vi.mock('../ui/mount', () => ({ mountSvelte: vi.fn() }));

import { activateTransferSurfaces, importFolderWithParentID } from './transfers';
import { get } from 'svelte/store';
import { uploadActivities } from '../ui/notifications/upload-activity-store';

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
    app.CreateFolderForDrive.mockImplementation(async (name: string, parentId: string) => {
        const folder = { id: `f${createdFolders.length + 1}`, name, parentId };
        createdFolders.push(folder);
        return { id: folder.id, name, parent_id: parentId };
    });
    app.RetryUploadToDriveFS.mockImplementation(async (paths: string[]) => ({
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
        expect(app.RetryUploadToDriveFS).toHaveBeenCalledTimes(3);
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
        const [paths, parentIDs] = app.RetryUploadToDriveFS.mock.calls[0];
        expect(paths).toEqual(['/cache/doc:0', '/cache/doc:1', '/cache/doc:2']);
        expect(parentIDs).toEqual(['f4', 'f3', 'f1']);
    });

    it('releases the window even when the upload throws', async () => {
        manifestJson = manifestOf('Holiday', ['a', 'b']);
        app.RetryUploadToDriveFS.mockRejectedValue(new Error('backend went away'));

        await importFolderWithParentID('root');

        expect(released).toEqual([['doc:0', 'doc:1']]);
        expect(mocks.markTransferDone).toHaveBeenCalledWith({ id: expect.stringMatching(/^(import|upload-)/), direction: 'up', status: 'failed' });
    });

    it('releases the window it was cancelled in and starts no more', async () => {
        manifestJson = manifestOf('Holiday', ['a', 'b', 'c', 'd', 'e']);
        app.RetryUploadToDriveFS.mockResolvedValue({
            result: { ok: false, error: { code: 'canceled', message: 'canceled' } },
            files: [],
        });

        await importFolderWithParentID('root');

        expect(app.RetryUploadToDriveFS).toHaveBeenCalledTimes(1);
        expect(released).toEqual([['doc:0', 'doc:1', 'doc:2', 'doc:3']]);
        expect(mocks.markTransferDone).toHaveBeenCalledWith({ id: expect.stringMatching(/^(import|upload-)/), direction: 'up', status: 'canceled' });
        // Backing out is not a failure to report.
        expect(mocks.notify).not.toHaveBeenCalled();
    });

    it('uploads the rest of a window when one file will not copy', async () => {
        manifestJson = manifestOf('Holiday', ['a', 'b', 'c']);
        unreadable = new Set(['doc:1']);

        await importFolderWithParentID('root');

        const [paths] = app.RetryUploadToDriveFS.mock.calls[0];
        expect(paths).toEqual(['/cache/doc:0', '/cache/doc:2']);
        // The id that never became a file is still released: a half-done copy
        // would otherwise sit in the cache forever.
        expect(released).toEqual([['doc:0', 'doc:1', 'doc:2']]);
        expect(mocks.markTransferDone).toHaveBeenCalledWith({ id: expect.stringMatching(/^(import|upload-)/), direction: 'up', status: 'failed' });
        expect(mocks.updateTransferName).toHaveBeenLastCalledWith({
            id: expect.stringMatching(/^(import|upload-)/),
            direction: 'up',
            name: 'Imported 2 files · 1 failed',
        });
    });

    it('lists every file of the folder with its own outcome', async () => {
        manifestJson = JSON.stringify({ root: 'Holiday', files: [
            { id: 'doc:0', rel: 'trip/a.jpg', size: 100 }, { id: 'doc:1', rel: 'trip/b.jpg', size: 100 },
            { id: 'doc:2', rel: 'notes/empty.txt', size: 0 }, { id: 'doc:3', rel: 'c.jpg', size: 100 },
        ] });
        unreadable = new Set(['doc:1']);

        await importFolderWithParentID('root');

        const [batch] = get(uploadActivities);
        // The empty file is skipped rather than listed as a failure.
        expect(batch.fileCount).toBe(3);
        expect(batch.files.map(({ name, path, status, message }) => ({ name, path, status, message }))).toEqual([
            { name: 'a.jpg', path: 'Holiday/trip/a.jpg', status: 'done', message: undefined },
            { name: 'b.jpg', path: 'Holiday/trip/b.jpg', status: 'failed', message: 'Could not be read from the folder.' },
            { name: 'c.jpg', path: 'Holiday/c.jpg', status: 'done', message: undefined },
        ]);
    });

    it('never copies or sends a row cancelled while it waited, and stops a sending row by its window id', async () => {
        manifestJson = manifestOf('Holiday', ['a', 'b', 'c', 'd', 'e', 'f']);
        let finishFirst = () => {};
        app.RetryUploadToDriveFS.mockImplementationOnce((paths: string[]) => new Promise((resolve) => {
            finishFirst = () => resolve({ result: { ok: true }, files: paths.map((path) => ({ name: path })) });
        }));
        const run = importFolderWithParentID('root');
        await vi.waitFor(() => expect(app.RetryUploadToDriveFS).toHaveBeenCalledOnce());
        const { cancelActivityFile } = await import('./transfers');
        const [batch] = get(uploadActivities);
        cancelActivityFile(batch.id, batch.files[1]);
        expect(mocks.cancelSingleUpload).toHaveBeenCalledWith(1);
        cancelActivityFile(batch.id, batch.files[5]);
        finishFirst();
        await run;

        expect(materialized).toEqual([['doc:0', 'doc:1', 'doc:2', 'doc:3'], ['doc:4']]);
        const files = get(uploadActivities)[0].files;
        expect(files[5].status).toBe('canceled');
        expect(files[4].status).toBe('done');
    });

    it('releases a window whose files all failed to copy', async () => {
        manifestJson = manifestOf('Holiday', ['a', 'b']);
        unreadable = new Set(['doc:0', 'doc:1']);

        await importFolderWithParentID('root');

        expect(app.RetryUploadToDriveFS).not.toHaveBeenCalled();
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
            id: expect.stringMatching(/^(import|upload-)/),
            direction: 'up',
            progress: 100,
            bytes: 1000,
            total: 1000,
            itemsDone: 2,
            itemsTotal: 2,
        });
        expect(mocks.markTransferDone).toHaveBeenCalledWith({ id: expect.stringMatching(/^(import|upload-)/), direction: 'up', status: 'done' });
    });

    it('starts the aggregate only after the selection is confirmed', async () => {
        await importFolderWithParentID('root');

        expect(mocks.pushTransferStart).toHaveBeenCalledWith({
            id: expect.stringMatching(/^(import|upload-)/),
            direction: 'up',
            name: 'Creating folders…',
            total: 100,
        });
    });

    it('does nothing at all when the picker is dismissed', async () => {
        manifestJson = '';

        await importFolderWithParentID('root');

        expect(app.CreateFolderForDrive).not.toHaveBeenCalled();
        expect(app.RetryUploadToDriveFS).not.toHaveBeenCalled();
        expect(mocks.openImportOptionsModal).not.toHaveBeenCalled();
        expect(mocks.notify).not.toHaveBeenCalled();
        expect(mocks.markTransferDone).not.toHaveBeenCalled();
    });

    it('uploads nothing when the import dialog is dismissed', async () => {
        mocks.openImportOptionsModal.mockResolvedValue(null);

        await importFolderWithParentID('root');

        expect(app.CreateFolderForDrive).not.toHaveBeenCalled();
        expect(materialized).toEqual([]);
        expect(mocks.markTransferDone).not.toHaveBeenCalled();
    });

    it('stops when the tree cannot be created, rather than uploading into the wrong place', async () => {
        manifestJson = manifestOf('Holiday', ['sub/a.jpg']);
        app.CreateFolderForDrive.mockRejectedValue(new Error('folder projection failed'));

        await importFolderWithParentID('root');

        expect(materialized).toEqual([]);
        expect(app.RetryUploadToDriveFS).not.toHaveBeenCalled();
        expect(mocks.markTransferDone).toHaveBeenCalledWith({ id: expect.stringMatching(/^(import|upload-)/), direction: 'up', status: 'failed' });
    });

    it('leaves the desktop picker alone', async () => {
        delete host.wails;
        app.SelectFolder.mockResolvedValue('');

        await importFolderWithParentID('root');

        expect(app.SelectFolder).toHaveBeenCalled();
        expect(materialized).toEqual([]);
    });
});

describe('Android document tree ownership', () => {
    it('keeps the native tree stable until the active folder run has released it', async () => {
        let finishUpload = (_result: unknown) => {};
        const pick = vi.spyOn(host.wails!, 'pickFolder');
        app.RetryUploadToDriveFS.mockImplementationOnce(() => new Promise((resolve) => { finishUpload = resolve; }))
            .mockResolvedValue({ result: { ok: true }, files: [{ name: 'a.jpg' }] });
        const first = importFolderWithParentID('root');
        await vi.waitFor(() => expect(app.RetryUploadToDriveFS).toHaveBeenCalledTimes(1));
        await importFolderWithParentID('root');
        expect(pick).toHaveBeenCalledTimes(1);
        expect(mocks.notify).toHaveBeenCalledWith(expect.objectContaining({ title: 'A folder is already selected' }));
        finishUpload({ result: { ok: true }, files: [{ name: 'a.jpg' }] });
        await first;
        await importFolderWithParentID('root');
        expect(pick).toHaveBeenCalledTimes(2);
    });

    it('ignores a canceled result from the previous dashboard lifetime', async () => {
        let finishUpload = (_result: unknown) => {};
        app.RetryUploadToDriveFS.mockImplementationOnce(() => new Promise((resolve) => { finishUpload = resolve; }));
        const old = importFolderWithParentID('root');
        await vi.waitFor(() => expect(app.RetryUploadToDriveFS).toHaveBeenCalledTimes(1));
        deactivateTransfers();
        deactivateTransfers = activateTransferSurfaces();
        state.cancelingUpload = false;
        state.importBatch = null;
        finishUpload({ result: { ok: false, error: { code: 'canceled', message: 'old session canceled' } }, files: [] });
        await old;
        expect(state.cancelingUpload).toBe(false);
        expect(state.importBatch).toBeNull();
    });
});
