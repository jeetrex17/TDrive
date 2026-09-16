/**
 * Where a phone says a download went.
 *
 * The interesting part is not the wording but the promise behind it: whatever
 * the toast names has to be somewhere the user can actually open. Android only
 * earns that by moving the file out of the sandbox first, so the move and the
 * message are tested together.
 */
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
    notify: vi.fn(),
    markTransferDone: vi.fn(),
    canSaveToDownloads: vi.fn(() => true),
    saveToDownloads: vi.fn(async () => 'Download/plan.pdf'),
    rememberDownloadSharePath: vi.fn(),
    android: false,
}));

const bindings = vi.hoisted(() => ({
    DownloadFile: vi.fn(),
    DownloadFolder: vi.fn(),
    SelectFiles: vi.fn(async () => []),
}));

const eventsOn = vi.hoisted(() => vi.fn(() => () => {}));

vi.mock('../../bindings/TDrive/app', () => bindings);
vi.mock('@wailsio/runtime', () => ({ Events: { On: eventsOn } }));
vi.mock('./notifications', () => ({ notify: mocks.notify }));
vi.mock('./android-downloads', () => ({
    canSaveToDownloads: mocks.canSaveToDownloads,
    saveToDownloads: mocks.saveToDownloads,
}));
vi.mock('../ui/mobile/mobile-shell-store', () => ({
    rememberDownloadSharePath: mocks.rememberDownloadSharePath,
}));
vi.mock('./notif-bell', () => ({
    markTransferDone: mocks.markTransferDone,
    pushTransferStart: vi.fn(),
    updateTransferName: vi.fn(),
    updateTransferProgress: vi.fn(),
    wasUploadCanceled: () => false,
}));
vi.mock('./encryption', () => ({ loadEncryptionStatus: vi.fn() }));
vi.mock('./modals/import-options', () => ({ openImportOptionsModal: vi.fn() }));
vi.mock('./modals/upload-options', () => ({ openUploadOptionsModal: vi.fn() }));
vi.mock('./modals/encryption-setup', () => ({ openEncryptionSetupModal: vi.fn() }));
vi.mock('./modals/encryption-password', () => ({ openEncryptionPasswordModal: vi.fn(async () => false) }));

vi.mock('../api', async (original) => ({
    ...(await original<Record<string, unknown>>()),
    isMobilePlatform: () => true,
    isAndroidPlatform: () => mocks.android,
}));

function success(savedPath: string) {
    return { result: { ok: true }, saved_path: savedPath };
}

async function loadModule() {
    vi.resetModules();
    const { idleTransferActivity, state } = await import('../state');
    state.downloadQueue = [];
    state.activeDownloadId = null;
    state.transferActivity = idleTransferActivity;
    state.cancelingDownload = false;
    return import('./transfers');
}

/** The body of the last toast, which is the sentence the user reads. */
function lastBody(): string {
    const calls = mocks.notify.mock.calls;
    return String((calls[calls.length - 1]?.[0] as { body?: unknown })?.body ?? '');
}

beforeEach(() => {
    vi.clearAllMocks();
    mocks.android = false;
    mocks.canSaveToDownloads.mockReturnValue(true);
    mocks.saveToDownloads.mockResolvedValue('Download/plan.pdf');
    bindings.DownloadFile.mockResolvedValue(success('/sandbox/Downloads/plan.pdf'));
    bindings.DownloadFolder.mockResolvedValue(success('/sandbox/Downloads/Holiday'));
});

describe('android', () => {
    beforeEach(() => { mocks.android = true; });

    it('moves a file to public Downloads and names where it landed', async () => {
        const mod = await loadModule();
        mod.enqueueDownload(42, 'plan.pdf', 10);

        await vi.waitFor(() => expect(mocks.saveToDownloads).toHaveBeenCalledWith('/sandbox/Downloads/plan.pdf'));
        await vi.waitFor(() => expect(lastBody()).toBe('Saved to Download/plan.pdf'));
    });

    it('moves a folder too, which is the case that had no way out at all', async () => {
        mocks.saveToDownloads.mockResolvedValue('Download/Holiday');
        const mod = await loadModule();
        mod.enqueueFolderDownload('d:holiday', 'Holiday');

        await vi.waitFor(() => expect(mocks.saveToDownloads).toHaveBeenCalledWith('/sandbox/Downloads/Holiday'));
        await vi.waitFor(() => expect(lastBody()).toBe('Saved to Download/Holiday'));
    });

    it('says the file is stranded rather than claiming a folder it is not in', async () => {
        mocks.saveToDownloads.mockRejectedValue(new Error('no space left'));
        const mod = await loadModule();
        mod.enqueueDownload(42, 'plan.pdf', 10);

        // The bytes did arrive, so this is a warning about where they are.
        await vi.waitFor(() => expect(mocks.notify).toHaveBeenCalledWith(
            expect.objectContaining({ level: 'warning', body: expect.stringContaining('could not be moved') }),
        ));
    });

    it('falls back to a plain confirmation when the host cannot move it', async () => {
        mocks.canSaveToDownloads.mockReturnValue(false);
        const mod = await loadModule();
        mod.enqueueDownload(42, 'plan.pdf', 10);

        await vi.waitFor(() => expect(mocks.notify).toHaveBeenCalled());
        expect(mocks.saveToDownloads).not.toHaveBeenCalled();
    });
});

describe('ios', () => {
    it('points at Files, and never at the sandbox path', async () => {
        const mod = await loadModule();
        mod.enqueueDownload(42, 'plan.pdf', 10);

        await vi.waitFor(() => expect(lastBody()).toContain('Files'));
        expect(lastBody()).not.toContain('/sandbox');
        expect(mocks.saveToDownloads).not.toHaveBeenCalled();
    });

    it('remembers a file so the transfers tab can share it again', async () => {
        const mod = await loadModule();
        mod.enqueueDownload(42, 'plan.pdf', 10);

        await vi.waitFor(() => expect(mocks.rememberDownloadSharePath).toHaveBeenCalledWith(
            'xfer:down:file:42', '/sandbox/Downloads/plan.pdf',
        ));
    });

    it('offers a folder no share sheet, because no phone share sheet takes one', async () => {
        const mod = await loadModule();
        mod.enqueueFolderDownload('d:holiday', 'Holiday');

        await vi.waitFor(() => expect(lastBody()).toContain('Files'));
        expect(lastBody()).not.toContain('share sheet');
        expect(mocks.rememberDownloadSharePath).not.toHaveBeenCalled();
    });
});
