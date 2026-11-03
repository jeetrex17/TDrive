/** Local positions contain identifiers and seconds only, never media URLs or titles. */
const STORAGE_KEY = 'tdrive.playback-position.v1';
const MAX_ENTRIES = 500;
const SAVE_INTERVAL_MS = 5000;
export type ResumeStorage = Pick<Storage, 'getItem' | 'setItem'>;
type Entry = { key: string; seconds: number };
type PlaybackSample = { currentTime: number; duration: number; paused: boolean; loading: boolean };

export function playbackResumeKey(
    accountId: number,
    info: { channelId: number; fileId: number; revision?: number },
    sourceIdentity?: string,
): string | null {
    if (!Number.isSafeInteger(accountId) || accountId <= 0 || !Number.isSafeInteger(info.fileId) || info.fileId <= 0) return null;
    if (sourceIdentity) {
        if (!/^source:\d+:(self|user|bot|group|supergroup|channel):\d+$/.test(sourceIdentity) || sourceIdentity.length > 128) return null;
        return JSON.stringify([accountId, sourceIdentity, info.fileId, info.revision || 0]);
    }
    if (!Number.isSafeInteger(info.channelId) || info.channelId === 0) return null;
    return JSON.stringify([accountId, 'drive', info.channelId, info.fileId, info.revision || 0]);
}

function browserStorage(): ResumeStorage | null {
    try { return globalThis.localStorage ?? null; } catch { return null; }
}

function entries(storage: ResumeStorage | null): Entry[] {
    try {
        const raw = storage?.getItem(STORAGE_KEY);
        if (!raw || raw.length > 256_000) return [];
        const value: unknown = JSON.parse(raw);
        if (!Array.isArray(value)) return [];
        return value.slice(-MAX_ENTRIES).filter((item): item is Entry => item && typeof item.key === 'string'
            && item.key.length <= 512 && typeof item.seconds === 'number' && Number.isFinite(item.seconds) && item.seconds > 0);
    } catch { return []; }
}

/** One recorder belongs to one resolved player session, independent of the active drive. */
export class PlaybackResume {
    private pending: number;
    private restoring = false;
    private protectStartupZero = false;
    private ready = false;
    private finished = false;
    private latest: number | null = null;
    private lastSavedAt = -Infinity;
    private wasPaused = true;

    constructor(
        private readonly key: string | null,
        private readonly storage: ResumeStorage | null = browserStorage(),
        private readonly now: () => number = Date.now,
        requestedPosition = 0,
    ) {
        this.pending = Number.isFinite(requestedPosition) && requestedPosition > 0
            ? requestedPosition : entries(storage).find((entry) => entry.key === key)?.seconds ?? 0;
    }

    /** Returns a seek once metadata is ready. Startup/loading events must not erase a saved position. */
    observe(state: PlaybackSample): number | null {
        if (this.finished && !state.paused && !state.loading && state.currentTime < state.duration - 1) {
            this.finished = false;
            this.pending = 0;
            this.latest = null;
            this.lastSavedAt = -Infinity;
        }
        if (this.finished || !Number.isFinite(state.duration) || state.duration <= 0
            || !Number.isFinite(state.currentTime) || state.currentTime < 0) return null;
        if (!this.ready) {
            this.ready = true;
            if (this.pending > 0 && this.pending < state.duration) {
                this.restoring = true;
                return this.pending;
            }
        }
        if (this.restoring) {
            if (Math.abs(state.currentTime - this.pending) > 2) return null;
            this.restoring = false;
        }
        if (state.loading || (this.protectStartupZero && state.currentTime === 0)) return null;
        this.protectStartupZero = false;
        this.latest = Math.min(state.currentTime, state.duration);
        const pausedNow = state.paused && !this.wasPaused;
        this.wasPaused = state.paused;
        if (pausedNow || this.now() - this.lastSavedAt >= SAVE_INTERVAL_MS) this.flush();
        return null;
    }

    /** A browser may reject a seek despite metadata; preserve the old bookmark until playback advances. */
    restoreFailed(): void {
        this.restoring = false;
        this.protectStartupZero = true;
    }

    flush(): void {
        if (this.finished || this.latest === null || this.restoring) return;
        this.write(this.latest);
        this.lastSavedAt = this.now();
    }

    complete(): void {
        this.finished = true;
        this.write(0);
    }

    private write(seconds: number): void {
        if (!this.storage || !this.key) return;
        const retained = entries(this.storage).filter((entry) => entry.key !== this.key);
        const next = seconds > 0 ? [...retained, { key: this.key, seconds }].slice(-MAX_ENTRIES) : retained;
        try { this.storage.setItem(STORAGE_KEY, JSON.stringify(next)); } catch {
            // Disabled/full storage must never interrupt a class or block closing the player.
        }
    }
}
