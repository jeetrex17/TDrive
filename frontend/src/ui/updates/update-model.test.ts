import { describe, expect, it } from 'vitest';
import {
    DEFAULT_UPDATE_PREFS,
    compareVersions,
    formatPlatform,
    initialUpdateState,
    isVersionSkipped,
    menuBadge,
    parseUpdatePrefs,
    progressPercent,
    serializeUpdatePrefs,
    shouldAnnounce,
    type UpdateState,
} from './update-model';

function stateWith(overrides: Partial<UpdateState>): UpdateState {
    return { ...initialUpdateState('1.6.0'), ...overrides };
}

function release(version: string) {
    return {
        version,
        tag: `v${version}`,
        page_url: `https://github.com/x/y/releases/tag/v${version}`,
        published_at: '2026-08-25T09:18:39Z',
        asset_name: `TDrive-v${version}-macos-arm64.zip`,
        asset_size: 42_000_000,
    };
}

describe('parseUpdatePrefs', () => {
    it('returns defaults for empty or malformed input', () => {
        expect(parseUpdatePrefs(null)).toEqual(DEFAULT_UPDATE_PREFS);
        expect(parseUpdatePrefs('not json')).toEqual(DEFAULT_UPDATE_PREFS);
        expect(parseUpdatePrefs('{}')).toEqual(DEFAULT_UPDATE_PREFS);
    });

    it('keeps valid fields and drops invalid ones', () => {
        expect(parseUpdatePrefs('{"autoDownload":false,"skippedVersion":"1.7.0"}')).toEqual({
            autoDownload: false,
            skippedVersion: '1.7.0',
        });
        expect(parseUpdatePrefs('{"autoDownload":"yes","skippedVersion":3}')).toEqual(DEFAULT_UPDATE_PREFS);
    });

    it('round-trips through serialize', () => {
        const prefs = { autoDownload: false, skippedVersion: '2.0.0' };
        expect(parseUpdatePrefs(serializeUpdatePrefs(prefs))).toEqual(prefs);
    });
});

describe('compareVersions', () => {
    it('orders by numeric parts and ignores prefixes/suffixes', () => {
        expect(compareVersions('1.6.0', '1.7.0')).toBe(-1);
        expect(compareVersions('v1.7.0', '1.7.0')).toBe(0);
        expect(compareVersions('1.10.0', '1.9.0')).toBe(1);
        expect(compareVersions('1.7.0-rc.1', '1.7.0')).toBe(0);
        expect(compareVersions('2.0.0', '1.9.9')).toBe(1);
    });
});

describe('isVersionSkipped', () => {
    it('is true only when the skip covers the offered version', () => {
        expect(isVersionSkipped('1.7.0', '1.7.0')).toBe(true);
        expect(isVersionSkipped('1.7.0', '1.8.0')).toBe(true);
        expect(isVersionSkipped('1.8.0', '1.7.0')).toBe(false);
        expect(isVersionSkipped('1.7.0', '')).toBe(false);
    });
});

describe('progressPercent', () => {
    it('clamps and avoids divide-by-zero', () => {
        expect(progressPercent(0, 0)).toBe(0);
        expect(progressPercent(50, 100)).toBe(50);
        expect(progressPercent(200, 100)).toBe(100);
        expect(progressPercent(-5, 100)).toBe(0);
    });
});

describe('menuBadge', () => {
    it('maps phases to badges', () => {
        expect(menuBadge(stateWith({ phase: 'ready' }))).toBe('ready');
        expect(menuBadge(stateWith({ phase: 'installing' }))).toBe('ready');
        expect(menuBadge(stateWith({ phase: 'checking' }))).toBe('checking');
        expect(menuBadge(stateWith({ phase: 'available' }))).toBe('none');
        expect(menuBadge(stateWith({ phase: 'up_to_date', error: 'boom', error_stage: 'check' }))).toBe('error');
    });
});

describe('shouldAnnounce', () => {
    it('announces actionable, unskipped releases only', () => {
        const prefs = DEFAULT_UPDATE_PREFS;
        expect(shouldAnnounce(stateWith({ phase: 'available', latest: release('1.7.0') }), prefs)).toBe(true);
        expect(shouldAnnounce(stateWith({ phase: 'ready', latest: release('1.7.0') }), prefs)).toBe(true);
        expect(shouldAnnounce(stateWith({ phase: 'downloading', latest: release('1.7.0') }), prefs)).toBe(false);
        expect(shouldAnnounce(stateWith({ phase: 'up_to_date', latest: null }), prefs)).toBe(false);
        expect(
            shouldAnnounce(stateWith({ phase: 'available', latest: release('1.7.0') }), {
                autoDownload: true,
                skippedVersion: '1.7.0',
            }),
        ).toBe(false);
    });
});

describe('formatPlatform', () => {
    it('renders friendly OS names', () => {
        expect(formatPlatform({ version: '1', os: 'darwin', arch: 'arm64', dev_build: false })).toBe('macOS arm64');
        expect(formatPlatform({ version: '1', os: 'windows', arch: 'amd64', dev_build: false })).toBe('Windows amd64');
        expect(formatPlatform(null)).toBe('');
    });
});
