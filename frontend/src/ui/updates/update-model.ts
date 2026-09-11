// Pure, framework-free helpers for the updater UI. The Svelte store and panel
// build on these so the interesting logic (preference parsing, skip rules,
// progress maths, phase interpretation) is unit-testable without a DOM.

import type { AppVersion, UpdatePhase, UpdateRelease, UpdateSnapshot } from '../../types';

export type { UpdatePhase };
export type ReleaseInfo = UpdateRelease;
export type AppVersionInfo = AppVersion;
export type UpdateState = UpdateSnapshot;

export interface UpdatePrefs {
    // Auto-download the payload as soon as a check finds one. Restarting is
    // always a manual choice regardless.
    autoDownload: boolean;
    // Version string ("1.7.0") the user asked to skip; a newer release clears it.
    skippedVersion: string;
}

export const DEFAULT_UPDATE_PREFS: UpdatePrefs = {
    autoDownload: true,
    skippedVersion: '',
};

export const UPDATE_PREFS_STORAGE_KEY = 'tdrive.updates.v1';

export function initialUpdateState(currentVersion = ''): UpdateState {
    return {
        phase: 'idle',
        currentVersion,
        latest: null,
        installable: false,
        installHint: '',
        downloadedBytes: 0,
        totalBytes: 0,
        checkedAt: 0,
        error: '',
        errorStage: '',
    };
}

// parseUpdatePrefs is defensive: storage may hold anything, so unknown shapes
// fall back to the defaults field by field.
export function parseUpdatePrefs(raw: string | null): UpdatePrefs {
    if (!raw) return { ...DEFAULT_UPDATE_PREFS };
    try {
        const parsed = JSON.parse(raw) as Partial<UpdatePrefs>;
        return {
            autoDownload:
                typeof parsed.autoDownload === 'boolean'
                    ? parsed.autoDownload
                    : DEFAULT_UPDATE_PREFS.autoDownload,
            skippedVersion:
                typeof parsed.skippedVersion === 'string' ? parsed.skippedVersion : '',
        };
    } catch {
        return { ...DEFAULT_UPDATE_PREFS };
    }
}

export function serializeUpdatePrefs(prefs: UpdatePrefs): string {
    return JSON.stringify(prefs);
}

// compareVersions orders two dotted numeric-ish versions. Pre-release and
// build suffixes are ignored — enough to answer "is the latest newer than the
// one the user skipped", which is the only comparison the UI needs.
export function compareVersions(a: string, b: string): number {
    const na = numericParts(a);
    const nb = numericParts(b);
    const len = Math.max(na.length, nb.length);
    for (let i = 0; i < len; i++) {
        const diff = (na[i] ?? 0) - (nb[i] ?? 0);
        if (diff !== 0) return diff < 0 ? -1 : 1;
    }
    return 0;
}

function numericParts(version: string): number[] {
    const core = String(version).trim().replace(/^v/, '').split(/[-+]/, 1)[0];
    return core.split('.').map((part) => {
        const n = parseInt(part, 10);
        return Number.isFinite(n) ? n : 0;
    });
}

// isVersionSkipped is true only when the skipped version is the same or newer
// than what's on offer, so a brand-new release always resurfaces.
export function isVersionSkipped(latestVersion: string, skippedVersion: string): boolean {
    if (!skippedVersion || !latestVersion) return false;
    return compareVersions(skippedVersion, latestVersion) >= 0;
}

// progressPercent clamps to 0..100 and never divides by zero.
export function progressPercent(done: number, total: number): number {
    if (!(total > 0)) return 0;
    const pct = Math.round((done / total) * 100);
    return Math.max(0, Math.min(100, pct));
}

export function formatPublished(iso: string): string {
    if (!iso) return '';
    const date = new Date(iso);
    if (Number.isNaN(date.getTime())) return '';
    return date.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
}

export function formatPlatform(info: AppVersionInfo | null): string {
    if (!info) return '';
    const os =
        info.os === 'darwin' ? 'macOS' : info.os === 'windows' ? 'Windows' : info.os === 'linux' ? 'Linux' : info.os;
    return `${os} ${info.arch}`;
}

export type UpdateBadge = 'none' | 'ready' | 'checking' | 'error';

// menuBadge decides what the account-menu row advertises: a filled dot when a
// build is ready to install, a spinner while checking, a subtle error mark on
// a failed check, nothing otherwise.
export function menuBadge(state: UpdateState): UpdateBadge {
    switch (state.phase) {
        case 'ready':
        case 'installing':
        case 'installed':
            return 'ready';
        case 'checking':
            return 'checking';
        default:
            return state.error ? 'error' : 'none';
    }
}

// A newer release is worth a one-time toast only when it is actually
// actionable and the user hasn't skipped it.
export function shouldAnnounce(state: UpdateState, prefs: UpdatePrefs): boolean {
    if (!state.latest) return false;
    if (isVersionSkipped(state.latest.version, prefs.skippedVersion)) return false;
    return state.phase === 'available' || state.phase === 'ready';
}
