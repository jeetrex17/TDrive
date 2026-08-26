import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import { get } from 'svelte/store';

vi.mock('../../modules/updates', () => ({
    checkForUpdates: vi.fn(async () => undefined),
    downloadUpdate: vi.fn(async () => undefined),
    cancelUpdateDownload: vi.fn(),
    installUpdate: vi.fn(async () => undefined),
    openReleasePage: vi.fn(),
    getRestartRisks: vi.fn(async () => [] as string[]),
}));

import * as actions from '../../modules/updates';
import UpdatesPanel from './UpdatesPanel.svelte';
import { initialUpdateState, type UpdateState } from './update-model';
import { appVersionInfo, updatePrefs, updateState } from './update-store';

let component: Record<string, unknown> | null = null;
let host: HTMLElement | null = null;

function release(version: string, size = 42_000_000) {
    return {
        version,
        tag: `v${version}`,
        page_url: `https://github.com/x/y/releases/tag/v${version}`,
        published_at: '2026-08-25T09:18:39Z',
        asset_name: `TDrive-v${version}-macos-arm64.zip`,
        asset_size: size,
    };
}

function setState(overrides: Partial<UpdateState>): void {
    updateState.set({ ...initialUpdateState('1.6.0'), ...overrides });
}

function setup(): void {
    host = document.createElement('div');
    document.body.appendChild(host);
    component = mount(UpdatesPanel, { target: host, props: {} });
    flushSync();
}

function q(selector: string): HTMLElement | null {
    return host?.querySelector<HTMLElement>(selector) ?? null;
}

function byText(text: string): HTMLElement | null {
    const nodes = Array.from(host?.querySelectorAll<HTMLElement>('button, .updates-line, .updates-headline') ?? []);
    return nodes.find((n) => n.textContent?.includes(text)) ?? null;
}

beforeEach(() => {
    updateState.set(initialUpdateState('1.6.0'));
    updatePrefs.set({ autoDownload: true, skippedVersion: '' });
    appVersionInfo.set({ version: '1.6.0', os: 'darwin', arch: 'arm64', dev_build: false });
    vi.clearAllMocks();
});

afterEach(async () => {
    if (component) await unmount(component);
    host?.remove();
    component = null;
    host = null;
});

describe('UpdatesPanel', () => {
    it('shows the current version and platform', () => {
        setup();
        expect(host?.textContent).toContain('Version 1.6.0');
        expect(host?.textContent).toContain('macOS arm64');
    });

    it('offers a download for an available release and can skip it', async () => {
        setState({ phase: 'available', installable: true, latest: release('1.7.0'), total_bytes: 42_000_000 });
        setup();

        const download = byText('Download');
        expect(download).not.toBeNull();
        download!.click();
        flushSync();
        expect(actions.downloadUpdate).toHaveBeenCalledOnce();

        byText('Skip this version')!.click();
        flushSync();
        expect(get(updatePrefs).skippedVersion).toBe('1.7.0');
    });

    it('renders download progress and cancels', () => {
        setState({ phase: 'downloading', latest: release('1.7.0'), downloaded_bytes: 21_000_000, total_bytes: 42_000_000 });
        setup();

        const bar = q('.updates-progress');
        expect(bar?.getAttribute('aria-valuenow')).toBe('50');
        expect((q('.updates-progress-fill') as HTMLElement).style.width).toBe('50%');

        byText('Cancel')!.click();
        flushSync();
        expect(actions.cancelUpdateDownload).toHaveBeenCalledOnce();
    });

    it('installs immediately when a restart carries no risks', async () => {
        vi.mocked(actions.getRestartRisks).mockResolvedValueOnce([]);
        setState({ phase: 'ready', installable: true, latest: release('1.7.0') });
        setup();

        byText('Restart to update')!.click();
        await tick();
        await Promise.resolve();
        await Promise.resolve();
        expect(actions.installUpdate).toHaveBeenCalledOnce();
    });

    it('asks for confirmation when a restart would interrupt work', async () => {
        vi.mocked(actions.getRestartRisks).mockResolvedValueOnce(['An upload is in progress and will be cancelled.']);
        setState({ phase: 'ready', installable: true, latest: release('1.7.0') });
        setup();

        byText('Restart to update')!.click();
        await tick();
        await Promise.resolve();
        await Promise.resolve();
        flushSync();

        expect(actions.installUpdate).not.toHaveBeenCalled();
        expect(host?.textContent).toContain('An upload is in progress');

        byText('Restart anyway')!.click();
        flushSync();
        expect(actions.installUpdate).toHaveBeenCalledOnce();
    });

    it('opens the release page for What\'s new', async () => {
        setState({ phase: 'ready', installable: true, latest: release('1.7.0') });
        setup();
        byText("What's new")!.click();
        flushSync();
        expect(actions.openReleasePage).toHaveBeenCalledOnce();
    });

    it('confirms an up-to-date state', () => {
        setState({ phase: 'up_to_date', checked_at: Date.now() });
        setup();
        expect(host?.textContent).toContain("You're on the latest version.");
    });

    it('surfaces a check error', () => {
        setState({ phase: 'available', installable: true, latest: release('1.7.0'), error: 'Couldn\'t reach GitHub.', error_stage: 'check' });
        setup();
        expect(q('.updates-error')?.textContent).toContain("Couldn't reach GitHub.");
    });

    it('falls back to the release page when the platform is not installable', () => {
        setState({ phase: 'available', installable: false, install_hint: 'This release has no macOS build yet.', latest: release('1.7.0') });
        setup();
        expect(host?.textContent).toContain('This release has no macOS build yet.');
        byText('Get it from GitHub')!.click();
        flushSync();
        expect(actions.openReleasePage).toHaveBeenCalledOnce();
    });

    it('toggles auto-download through preferences', () => {
        setState({ phase: 'up_to_date' });
        setup();
        const toggle = host?.querySelector<HTMLInputElement>('.updates-toggle input');
        expect(toggle?.checked).toBe(true);
        toggle!.click();
        flushSync();
        expect(get(updatePrefs).autoDownload).toBe(false);
    });

    it('hides update actions for a development build', () => {
        setState({ phase: 'disabled' });
        setup();
        expect(host?.textContent).toContain('development build');
        expect(host?.querySelector('.updates-footer')).toBeNull();
    });
});
