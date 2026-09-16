import {
    closeMedia,
    closeNativeMedia,
    nativeMediaCommand,
    onRuntimeEvent,
    type MediaOpenResult,
    type NativeMediaOpenResult,
    type NativeMediaStatePayload,
} from '../../api';
import {
    nativePreferenceCommands,
    type PlaybackPreferences,
} from './playback-preferences';
import { normalizeNativeTracks, type NativeMediaTrack } from './media-tracks';

export const MIN_PLAYBACK_RATE = 0.25;
export const MAX_PLAYBACK_RATE = 4;

export interface BufferedRange {
    start: number;
    end: number;
}

export interface PlayerState {
    paused: boolean;
    currentTime: number;
    duration: number;
    buffered: BufferedRange[];
    volume: number;
    muted: boolean;
    rate: number;
    loading: boolean;
    tracks: NativeMediaTrack[];
}

export interface PlayerAdapter {
    subscribe(callback: (state: PlayerState) => void): () => void;
    playPause(): void;
    seekAbsolute(seconds: number): void;
    seekRelative(seconds: number): void;
    setVolume(value: number): void;
    setMuted(value: boolean): void;
    setSpeed(value: number): void;
    close(): Promise<void>;
}

export const EMPTY_PLAYER_STATE: PlayerState = {
    paused: true,
    currentTime: 0,
    duration: 0,
    buffered: [],
    volume: 1,
    muted: false,
    rate: 1,
    loading: false,
    tracks: [],
};

interface HtmlVideoAdapterCallbacks {
    mediaError(code: number | undefined, state: PlayerState): void;
    playbackError(message: string): void;
    revealChrome(): void;
    mediaEnded(): void;
}

interface NativeMpvAdapterCallbacks {
    mediaError(detail: string): void;
    mediaClosed(): void;
    mediaEnded(): void;
    dispose(adapter: NativeMpvAdapter): void;
}

function clamp(value: number, min: number, max: number): number {
    return Math.max(min, Math.min(max, Number.isFinite(value) ? value : min));
}

export function clampPlaybackRate(value: number): number {
    return clamp(Number.isFinite(value) ? value : 1, MIN_PLAYBACK_RATE, MAX_PLAYBACK_RATE);
}

function keepUsefulBufferedRanges(ranges: BufferedRange[], currentTime: number): BufferedRange[] {
    return ranges.filter((range) => range.end >= currentTime - 2);
}

function bufferedRanges(video: HTMLVideoElement): BufferedRange[] {
    const ranges: BufferedRange[] = [];
    const buffered = video.buffered;
    for (let i = 0; i < buffered.length; i += 1) {
        const start = buffered.start(i);
        const end = buffered.end(i);
        if (Number.isFinite(start) && Number.isFinite(end) && end > start) {
            ranges.push({ start, end });
        }
    }
    return ranges;
}

function nativePayloadToState(payload: NativeMediaStatePayload, previous: PlayerState): PlayerState {
    const duration = Number(payload.duration ?? previous.duration ?? 0);
    const currentTime = Number(payload.current_time ?? previous.currentTime ?? 0);
    const volume = Number(payload.volume ?? previous.volume ?? 1);
    const rate = Number(payload.rate ?? previous.rate ?? 1);
    const buffered = Array.isArray(payload.buffered)
        ? payload.buffered
            .map((range) => ({
                start: Number(range.start ?? 0),
                end: Number(range.end ?? 0),
            }))
            .filter((range) => Number.isFinite(range.start) && Number.isFinite(range.end) && range.end > range.start)
        : previous.buffered;
    return {
        paused: Boolean(payload.paused ?? previous.paused),
        currentTime: clamp(currentTime, 0, Math.max(0, duration || currentTime)),
        duration: Math.max(0, Number.isFinite(duration) ? duration : 0),
        buffered,
        volume: clamp(volume, 0, 1),
        muted: Boolean(payload.muted ?? previous.muted),
        rate: clamp(rate, MIN_PLAYBACK_RATE, MAX_PLAYBACK_RATE),
        loading: Boolean(payload.loading ?? false),
        tracks: Array.isArray(payload.tracks) ? normalizeNativeTracks(payload.tracks) : previous.tracks,
    };
}

function nativeFailureDetail(payload: NativeMediaStatePayload): string | null {
    const detail = typeof payload.error === 'string' ? payload.error.trim().slice(0, 256) : '';
    if (payload.status !== 'failed' && !detail) return null;
    return detail || 'native media player exited unexpectedly';
}

function normalizeNativeMediaStatePayload(value: unknown): NativeMediaStatePayload | null {
    if (!value || typeof value !== 'object') return null;
    const raw = value as NativeMediaStatePayload;
    const token = typeof raw.token === 'string' ? raw.token : '';
    if (!token || token.length > 512 || token.trim() !== token) return null;
    const sequence = Number(raw.sequence ?? 0);
    return {
        ...raw,
        token,
        sequence: Number.isSafeInteger(sequence) && sequence > 0 ? sequence : 0,
    };
}

function nativeStateSequence(payload: NativeMediaStatePayload | null | undefined): number {
    const sequence = Number(payload?.sequence ?? 0);
    return Number.isSafeInteger(sequence) && sequence > 0 ? sequence : 0;
}

const BUFFERED_MERGE_GAP_SECONDS = 0.4;

// Native and HTML engines both expose disjoint cached ranges. Clamp, sort, and
// merge only fragment-boundary hairlines so the timeline stays honest.
export function coalesceBufferedRanges(ranges: BufferedRange[], duration: number): BufferedRange[] {
    const sorted = ranges
        .map((range) => ({ start: clamp(range.start, 0, duration), end: clamp(range.end, 0, duration) }))
        .filter((range) => range.end > range.start)
        .sort((a, b) => a.start - b.start);
    const merged: BufferedRange[] = [];
    for (const range of sorted) {
        const last = merged[merged.length - 1];
        if (last && range.start - last.end <= BUFFERED_MERGE_GAP_SECONDS) {
            last.end = Math.max(last.end, range.end);
        } else {
            merged.push({ ...range });
        }
    }
    return merged;
}

export class HtmlVideoAdapter implements PlayerAdapter {
    private readonly subscribers = new Set<(state: PlayerState) => void>();
    private readonly listeners: Array<() => void> = [];
    private closed = false;
    private failureReported = false;
    private naturalEndReported = false;
    private lastAudibleVolume: number;

    constructor(
        private readonly video: HTMLVideoElement,
        private readonly opened: MediaOpenResult,
        private readonly callbacks: HtmlVideoAdapterCallbacks,
    ) {
        this.lastAudibleVolume = video.volume > 0 ? video.volume : 1;
        const events = [
            'loadstart',
            'loadedmetadata',
            'canplay',
            'waiting',
            'playing',
            'pause',
            'timeupdate',
            'durationchange',
            'progress',
            'volumechange',
            'ratechange',
            'seeking',
            'seeked',
        ];
        for (const event of events) {
            const listener = () => this.emit();
            video.addEventListener(event, listener);
            this.listeners.push(() => video.removeEventListener(event, listener));
        }
        const errorListener = () => {
            if (this.closed || this.failureReported || this.naturalEndReported) return;
            // Chromium also fires `error` on the element when its poster image
            // fails to load; without a MediaError there is no playback failure.
            if (!this.video.error) return;
            this.failureReported = true;
            this.callbacks.mediaError(this.video.error?.code, this.snapshot());
        };
        video.addEventListener('error', errorListener);
        this.listeners.push(() => video.removeEventListener('error', errorListener));
        const endedListener = () => {
            if (this.closed || this.failureReported || this.naturalEndReported) return;
            this.naturalEndReported = true;
            this.callbacks.mediaEnded();
        };
        video.addEventListener('ended', endedListener);
        this.listeners.push(() => video.removeEventListener('ended', endedListener));
    }

    load(): void {
        this.video.pause();
        this.video.removeAttribute('src');
        this.video.load();
        this.video.src = this.opened.url;
        this.video.playbackRate = 1;
        this.emit();

        const playPromise = this.video.play();
        if (playPromise && typeof playPromise.catch === 'function') {
            playPromise.catch(() => {
                // Autoplay may be blocked; leave the first frame and explicit
                // play control visible.
                this.emit();
                this.callbacks.revealChrome();
            });
        }
    }

    subscribe(callback: (state: PlayerState) => void): () => void {
        this.subscribers.add(callback);
        callback(this.snapshot());
        return () => this.subscribers.delete(callback);
    }

    playPause(): void {
        if (this.video.paused) {
            this.video.play().catch((error) => {
                this.callbacks.playbackError(String(error?.message || error || 'Playback failed.'));
            });
        } else {
            this.video.pause();
        }
        this.emit();
    }

    seekAbsolute(seconds: number): void {
        const duration = Number.isFinite(this.video.duration) ? this.video.duration : 0;
        if (duration <= 0) return;
        this.video.currentTime = clamp(seconds, 0, duration);
        this.emit();
    }

    seekRelative(seconds: number): void {
        this.seekAbsolute(this.video.currentTime + seconds);
    }

    setVolume(value: number): void {
        const next = clamp(value, 0, 1);
        this.video.volume = next;
        if (next > 0) this.lastAudibleVolume = next;
        this.video.muted = next === 0;
        this.emit();
    }

    setMuted(value: boolean): void {
        if (!value && this.video.volume === 0) {
            this.video.volume = this.lastAudibleVolume;
        }
        this.video.muted = value;
        this.emit();
    }

    setSpeed(value: number): void {
        this.video.playbackRate = clampPlaybackRate(value);
        this.emit();
    }

    async close(): Promise<void> {
        if (this.closed) return;
        try {
            this.detach();
        } finally {
            await closeMedia(this.opened.token);
        }
    }

    detachForNative(): MediaOpenResult | null {
        return this.detach() ? this.opened : null;
    }

    private detach(): boolean {
        if (this.closed) return false;
        this.closed = true;
        for (const remove of this.listeners.splice(0)) remove();
        this.subscribers.clear();
        try {
            this.video.pause();
        } catch {
            // The token still has to be released if a platform media element is
            // already torn down and rejects a final pause/reset.
        }
        this.video.removeAttribute('src');
        try {
            this.video.load();
        } catch {
            // Removing src is sufficient to detach ownership for native handoff.
        }
        return true;
    }

    private snapshot(): PlayerState {
        const duration = Number.isFinite(this.video.duration) ? this.video.duration : 0;
        const currentTime = Number.isFinite(this.video.currentTime) ? this.video.currentTime : 0;
        return {
            paused: this.video.paused,
            currentTime,
            duration,
            buffered: bufferedRanges(this.video),
            volume: this.video.volume,
            muted: this.video.muted || this.video.volume === 0,
            rate: this.video.playbackRate || 1,
            loading: this.video.readyState < HTMLMediaElement.HAVE_FUTURE_DATA && !this.video.paused,
            tracks: [],
        };
    }

    private emit(): void {
        if (this.closed) return;
        const state = this.snapshot();
        for (const callback of this.subscribers) callback(state);
    }
}

export class NativeMpvAdapter implements PlayerAdapter {
    private readonly subscribers = new Set<(state: PlayerState) => void>();
    private state: PlayerState = { ...EMPTY_PLAYER_STATE, paused: false, loading: true };
    private closed = false;
    private commandFlushFrame = 0;
    private pendingSeek: { mode: 'absolute' | 'relative'; value: number } | null = null;
    private readonly pendingLatestCommands = new Map<string, string[]>();
    private lastAudibleVolume = 1;
    private failureReported = false;
    private closeReported = false;
    private naturalEndReported = false;
    private lastSequence = 0;
    private pendingPreferences: PlaybackPreferences | null = null;
    private applyingPreferences = false;

    constructor(
        private readonly opened: NativeMediaOpenResult,
        private readonly callbacks: NativeMpvAdapterCallbacks,
    ) {}

    get token(): string {
        return this.opened.token;
    }

    start(pending: NativeMediaStatePayload | null): void {
        if (this.opened.initialState) this.applyPayload(this.opened.initialState);
        if (pending) this.applyPayload(pending);
    }

    accepts(payload: NativeMediaStatePayload): boolean {
        return !this.closed && payload.token === this.opened.token;
    }

    receive(payload: NativeMediaStatePayload): void {
        if (this.accepts(payload)) this.applyPayload(payload);
    }

    isTerminal(): boolean {
        return this.failureReported || this.closeReported;
    }

    subscribe(callback: (state: PlayerState) => void): () => void {
        this.subscribers.add(callback);
        callback(this.state);
        return () => this.subscribers.delete(callback);
    }

    playPause(): void {
        void this.sendCommand(['cycle', 'pause']);
        this.updateFallbackState((state) => ({ ...state, paused: !state.paused }));
    }

    setPaused(value: boolean): void {
        const paused = Boolean(value);
        this.scheduleLatestCommand('pause', ['set', 'pause', paused ? 'yes' : 'no']);
        this.updateFallbackState((state) => ({ ...state, paused }));
    }

    seekAbsolute(seconds: number): void {
        if (this.state.duration <= 0) return;
        const next = clamp(seconds, 0, Math.max(0, this.state.duration || seconds));
        this.scheduleSeek('absolute', next);
        this.updateFallbackState((state) => ({
            ...state,
            currentTime: clamp(next, 0, Math.max(0, state.duration || next)),
            buffered: keepUsefulBufferedRanges(state.buffered, next),
        }));
    }

    seekRelative(seconds: number): void {
        this.scheduleSeek('relative', seconds);
        this.updateFallbackState((state) => {
            if (state.duration <= 0) return state;
            const currentTime = clamp(state.currentTime + seconds, 0, state.duration);
            return { ...state, currentTime, buffered: keepUsefulBufferedRanges(state.buffered, currentTime) };
        });
    }

    setVolume(value: number): void {
        const next = clamp(value, 0, 1);
        this.scheduleLatestCommand('volume', ['set', 'volume', String(Math.round(next * 100))]);
        if (next > 0) {
            this.lastAudibleVolume = next;
            this.scheduleLatestCommand('mute', ['set', 'mute', 'no']);
        }
        this.updateFallbackState((state) => ({ ...state, volume: next, muted: next <= 0 }));
    }

    setMuted(value: boolean): void {
        const nextVolume = !value && this.state.volume === 0 ? this.lastAudibleVolume : this.state.volume;
        if (!value && this.state.volume === 0) {
            this.scheduleLatestCommand('volume', ['set', 'volume', String(Math.round(nextVolume * 100))]);
        }
        this.scheduleLatestCommand('mute', ['set', 'mute', value ? 'yes' : 'no']);
        this.updateFallbackState((state) => ({ ...state, volume: nextVolume, muted: value }));
    }

    setSpeed(value: number): void {
        const next = clampPlaybackRate(value);
        this.scheduleLatestCommand('speed', ['set', 'speed', String(next)]);
        this.updateFallbackState((state) => ({ ...state, rate: next }));
    }

    applyPreferences(preferences: PlaybackPreferences): void {
        if (this.closed) return;
        this.pendingPreferences = preferences;
        if (!this.applyingPreferences) void this.flushPreferences();
    }

    setAudioTrack(id: number): void {
        if (!Number.isSafeInteger(id) || id <= 0) return;
        void this.sendCommand(['set', 'aid', String(id)]);
        this.updateTrackSelection('audio', id);
    }

    setSubtitleTrack(id: number | null): void {
        if (id !== null && (!Number.isSafeInteger(id) || id <= 0)) return;
        void this.sendCommand(['set', 'sid', id === null ? 'no' : String(id)]);
        this.updateTrackSelection('subtitle', id);
    }

    async close(): Promise<void> {
        if (this.closed) return;
        this.closed = true;
        this.clearScheduledCommands();
        this.pendingPreferences = null;
        this.callbacks.dispose(this);
        this.subscribers.clear();
        await closeNativeMedia(this.opened.token);
    }

    private applyPayload(payload: NativeMediaStatePayload): void {
        if (this.closed || this.failureReported || this.closeReported) return;
        const sequence = Number(payload.sequence ?? 0);
        if (sequence > 0) {
            if (sequence <= this.lastSequence) return;
            this.lastSequence = sequence;
        }
        const failure = nativeFailureDetail(payload);
        if (failure) {
            if (!this.failureReported) {
                this.failureReported = true;
                this.callbacks.mediaError(failure);
            }
            return;
        }
        if (payload.status === 'closed') {
            if (!this.closeReported) {
                this.closeReported = true;
                this.callbacks.mediaClosed();
            }
            return;
        }
        const naturalEnd = payload.status === 'ended' || payload.eof === true;
        const shouldReportNaturalEnd = naturalEnd && !this.naturalEndReported;
        if (shouldReportNaturalEnd) this.naturalEndReported = true;
        this.state = nativePayloadToState(payload, this.state);
        if (!this.state.muted && this.state.volume > 0) this.lastAudibleVolume = this.state.volume;
        this.emit();
        if (shouldReportNaturalEnd) this.callbacks.mediaEnded();
    }

    private async flushPreferences(): Promise<void> {
        this.applyingPreferences = true;
        try {
            while (this.pendingPreferences && !this.closed) {
                const preferences = this.pendingPreferences;
                this.pendingPreferences = null;
                for (const command of nativePreferenceCommands(preferences)) {
                    if (this.closed) return;
                    // Complete each batch in order; rapid edits coalesce to the
                    // latest next batch.
                    await this.sendCommand(command);
                }
            }
        } finally {
            this.applyingPreferences = false;
        }
    }

    private updateTrackSelection(type: NativeMediaTrack['type'], id: number | null): void {
        this.updateFallbackState((state) => ({
            ...state,
            tracks: state.tracks.map((track) => (
                track.type === type ? { ...track, selected: track.id === id } : track
            )),
        }));
    }

    private emit(): void {
        for (const callback of this.subscribers) callback(this.state);
    }

    private updateFallbackState(update: (state: PlayerState) => PlayerState): void {
        if (this.closed) return;
        this.state = update(this.state);
        if (!this.state.muted && this.state.volume > 0) this.lastAudibleVolume = this.state.volume;
        this.emit();
    }

    private scheduleSeek(mode: 'absolute' | 'relative', value: number): void {
        if (mode === 'relative' && this.pendingSeek?.mode === 'relative') {
            this.pendingSeek.value += value;
        } else {
            this.pendingSeek = { mode, value };
        }
        this.scheduleCommandFlush();
    }

    private scheduleLatestCommand(key: string, command: string[]): void {
        this.pendingLatestCommands.set(key, command);
        this.scheduleCommandFlush();
    }

    private scheduleCommandFlush(): void {
        if (this.commandFlushFrame || this.closed) return;
        this.commandFlushFrame = requestAnimationFrame(() => {
            this.commandFlushFrame = 0;
            this.flushScheduledCommands();
        });
    }

    private flushScheduledCommands(): void {
        if (this.closed) return;
        const seek = this.pendingSeek;
        this.pendingSeek = null;
        if (seek) void this.sendCommand(['seek', String(seek.value), seek.mode]);
        const commands = Array.from(this.pendingLatestCommands.values());
        this.pendingLatestCommands.clear();
        for (const command of commands) void this.sendCommand(command);
    }

    private clearScheduledCommands(): void {
        if (this.commandFlushFrame) {
            cancelAnimationFrame(this.commandFlushFrame);
            this.commandFlushFrame = 0;
        }
        this.pendingSeek = null;
        this.pendingLatestCommands.clear();
    }

    private async sendCommand(command: string[]): Promise<void> {
        if (this.closed) return;
        try {
            await nativeMediaCommand(this.opened.token, command);
        } catch (error) {
            console.warn('NativeMediaCommand failed:', error);
        }
    }
}

export class NativeMediaStateRouter {
    private active: NativeMpvAdapter | null = null;
    private readonly pending = new Map<string, NativeMediaStatePayload>();
    private unsubscribe: (() => void) | null = null;

    bind(): void {
        if (this.unsubscribe) return;
        this.unsubscribe = onRuntimeEvent('native_media_state', (value) => this.route(value));
    }

    unbind(): void {
        this.unsubscribe?.();
        this.unsubscribe = null;
        this.active = null;
        this.pending.clear();
    }

    activate(adapter: NativeMpvAdapter): boolean {
        this.active = adapter;
        adapter.start(this.takePending(adapter.token));
        return adapter.isTerminal();
    }

    deactivate(adapter: NativeMpvAdapter): void {
        if (this.active === adapter) this.active = null;
        this.pending.delete(adapter.token);
    }

    discard(token: string): void {
        this.pending.delete(token);
    }

    private route(value: unknown): void {
        const payload = normalizeNativeMediaStatePayload(value);
        if (!payload) return;
        if (this.active?.accepts(payload)) {
            this.active.receive(payload);
            return;
        }
        this.cachePending(payload);
    }

    private cachePending(payload: NativeMediaStatePayload): void {
        const token = payload.token!;
        const previous = this.pending.get(token);
        const sequence = nativeStateSequence(payload);
        const previousSequence = nativeStateSequence(previous);
        if (previous && previousSequence > 0 && (sequence === 0 || sequence <= previousSequence)) return;
        this.pending.delete(token);
        this.pending.set(token, payload);
        while (this.pending.size > 16) {
            const oldest = this.pending.keys().next().value;
            if (typeof oldest !== 'string') break;
            this.pending.delete(oldest);
        }
    }

    private takePending(token: string): NativeMediaStatePayload | null {
        const payload = this.pending.get(token) ?? null;
        this.pending.delete(token);
        return payload;
    }
}
