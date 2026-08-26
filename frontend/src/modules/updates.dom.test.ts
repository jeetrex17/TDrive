import { beforeEach, describe, expect, it, vi } from 'vitest';

// Backend bindings and the toast surface are mocked; the module factories are
// hoisted so both static and dynamic (per-test) imports see them.
const bindings = vi.hoisted(() => ({
    AppVersion: vi.fn(async () => ({ version: '1.6.0', os: 'darwin', arch: 'arm64', dev_build: false })),
    CancelUpdateDownload: vi.fn(async () => undefined),
    CheckForUpdate: vi.fn(),
    DownloadUpdate: vi.fn(async () => undefined),
    GetUpdateState: vi.fn(async () => ({ phase: 'idle' })),
    InstallUpdateAndRestart: vi.fn(async () => undefined),
    MountStatus: vi.fn(async () => ({ mounted: false })),
    OpenUpdatePage: vi.fn(async () => undefined),
}));
const notifyMock = vi.hoisted(() => vi.fn());

vi.mock('../../wailsjs/go/main/App', () => bindings);
vi.mock('./notifications', () => ({ notify: notifyMock }));

import { initialUpdateState, type UpdateState } from '../ui/updates/update-model';

function available(version = '1.7.0'): UpdateState {
    return {
        ...initialUpdateState('1.6.0'),
        phase: 'available',
        installable: true,
        latest: {
            version,
            tag: `v${version}`,
            page_url: 'https://github.com/x/y',
            published_at: '',
            asset_name: `TDrive-v${version}-macos-arm64.zip`,
            asset_size: 10,
        },
        total_bytes: 10,
    };
}

// resetModules gives the freshly imported updater its own copy of ../state,
// so grab that same instance to drive transfer/vault conditions.
async function loadModule() {
    vi.resetModules();
    const { state } = await import('../state');
    state.activeTransfer = null;
    state.encryption = { available: false, passwordSet: false, passwordRemembered: false, hint: '', loaded: true };
    const mod = await import('./updates');
    const store = await import('../ui/updates/update-store');
    return { mod, store, state };
}

beforeEach(() => {
    vi.clearAllMocks();
});

describe('update policy', () => {
    it('auto-downloads an eligible release exactly once', async () => {
        bindings.CheckForUpdate.mockResolvedValue(available());
        const { mod } = await loadModule();

        await mod.checkForUpdates();
        await mod.checkForUpdates();

        expect(bindings.DownloadUpdate).toHaveBeenCalledOnce();
    });

    it('does not auto-download when the preference is off', async () => {
        bindings.CheckForUpdate.mockResolvedValue(available());
        const { mod, store } = await loadModule();
        store.setAutoDownload(false);

        await mod.checkForUpdates();

        expect(bindings.DownloadUpdate).not.toHaveBeenCalled();
    });

    it('does not auto-download a skipped version', async () => {
        bindings.CheckForUpdate.mockResolvedValue(available('1.7.0'));
        const { mod, store } = await loadModule();
        store.skipVersion('1.7.0');

        await mod.checkForUpdates();

        expect(bindings.DownloadUpdate).not.toHaveBeenCalled();
    });

    it('defers auto-download while a transfer is active', async () => {
        bindings.CheckForUpdate.mockResolvedValue(available());
        const { mod, state } = await loadModule();
        state.activeTransfer = 'upload';

        await mod.checkForUpdates();

        expect(bindings.DownloadUpdate).not.toHaveBeenCalled();
    });

    it('announces a new version once', async () => {
        bindings.CheckForUpdate.mockResolvedValue(available());
        const { mod, store } = await loadModule();
        store.setAutoDownload(false);

        await mod.checkForUpdates();
        await mod.checkForUpdates();

        const announcements = notifyMock.mock.calls.filter(([opts]) => String(opts.title).includes('1.7.0 is available'));
        expect(announcements).toHaveLength(1);
    });

    it('toasts "up to date" only for explicit checks', async () => {
        bindings.CheckForUpdate.mockResolvedValue({ ...initialUpdateState('1.6.0'), phase: 'up_to_date' });
        const { mod } = await loadModule();

        await mod.checkForUpdates();
        expect(notifyMock).not.toHaveBeenCalled();

        await mod.checkForUpdates({ explicit: true });
        expect(notifyMock).toHaveBeenCalledWith(expect.objectContaining({ level: 'success' }));
    });

    it('lists restart risks from transfer, mount, and vault state', async () => {
        bindings.MountStatus.mockResolvedValue({ mounted: true });
        const { mod, state } = await loadModule();
        state.activeTransfer = 'download';
        state.encryption = { available: true, passwordSet: true, passwordRemembered: true, hint: '', loaded: true };

        const risks = await mod.getRestartRisks();
        expect(risks).toHaveLength(3);
        expect(risks[0]).toContain('download');
        expect(risks.some((r) => r.includes('ejected'))).toBe(true);
        expect(risks.some((r) => r.includes('encryption password'))).toBe(true);
    });

    it('has no restart risks when idle', async () => {
        bindings.MountStatus.mockResolvedValue({ mounted: false });
        const { mod } = await loadModule();
        expect(await mod.getRestartRisks()).toEqual([]);
    });
});
