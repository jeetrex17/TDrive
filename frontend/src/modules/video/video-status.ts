/**
 * Everything the player says about itself while it is working: the buffering
 * spinner, the line under the filename, and the live stream rate behind both.
 *
 * The spinner is debounced because a seek that resolves from buffer would
 * otherwise flash it for a frame, and the stream rate is held briefly after the
 * backend stops reporting one so a momentary gap between range requests does not
 * blank the label mid-playback.
 */

import { getMediaStats, type MediaStats } from '../../api';
import { formatBytes } from '../../utils';
import { videoFormatLabel } from '../media-types';
import type { VideoDOM } from './video-dom';

const LOADING_DEBOUNCE_MS = 250;
const MEDIA_STATS_POLL_MS = 1000;
const STREAM_ACTIVITY_HOLD_MS = 2000;

const STREAMING_PREFIX = 'Streaming ';
const RATE_LIMITED = 'Rate-limited';

export interface VideoStatusContext {
    dom: VideoDOM;
    hasAdapter(): boolean;
    hasError(): boolean;
    /** The current item's duration, which turns a byte rate into a realtime multiple. */
    duration(): number;
}

export class VideoStatusController {
    private loadingTimer: ReturnType<typeof setTimeout> | null = null;
    private statusOverride = '';
    private statsTimer: number | null = null;
    private statsInFlight = false;
    private statsToken = '';
    private activityText = '';
    private activityAt = 0;
    private activityClearTimer: number | null = null;
    private metaBaseText = '';
    private metaBytes = 0;

    constructor(private readonly ctx: VideoStatusContext) {}

    setLoading(visible: boolean): void {
        const { loading, modal } = this.ctx.dom;
        if (!loading) return;
        modal?.classList.toggle('is-video-loading', visible);
        if (this.loadingTimer) {
            clearTimeout(this.loadingTimer);
            this.loadingTimer = null;
        }
        if (!visible) {
            loading.style.display = 'none';
            loading.setAttribute('aria-hidden', 'true');
            this.refreshStatus();
            return;
        }
        this.refreshStatus();
        this.loadingTimer = setTimeout(() => {
            this.loadingTimer = null;
            if (!loading || this.ctx.hasError()) return;
            loading.style.display = 'flex';
            loading.setAttribute('aria-hidden', 'false');
        }, LOADING_DEBOUNCE_MS);
    }

    /** A caller-supplied line that outranks the derived buffering text. */
    setOverride(message: string): void {
        this.statusOverride = message;
        this.refreshStatus();
    }

    /**
     * Drops the override without repainting: the caller is mid-state-apply and
     * will set the spinner, which repaints anyway.
     */
    clearOverride(): void {
        this.statusOverride = '';
    }

    setMediaText(name: string, size: number): void {
        const { filename } = this.ctx.dom;
        if (filename) filename.textContent = name || 'Video';
        this.metaBaseText = `${videoFormatLabel(name)}${size ? ` · ${formatBytes(size)}` : ''}`;
        this.metaBytes = size || 0;
        this.renderMeta();
    }

    /** Polls only while a session exists and nothing has failed. */
    syncPolling(token: string): void {
        this.statsToken = token;
        if (token && !this.ctx.hasError()) this.startPolling();
        else this.stopPolling();
    }

    stopPolling(): void {
        if (this.statsTimer != null) {
            window.clearInterval(this.statsTimer);
            this.statsTimer = null;
        }
        this.clearActivity();
        this.refreshStatus();
    }

    destroy(): void {
        if (this.loadingTimer) {
            clearTimeout(this.loadingTimer);
            this.loadingTimer = null;
        }
        this.stopPolling();
    }

    private startPolling(): void {
        if (this.statsTimer != null) return;
        void this.pollStats();
        this.statsTimer = window.setInterval(() => {
            void this.pollStats();
        }, MEDIA_STATS_POLL_MS);
    }

    private async pollStats(): Promise<void> {
        if (!this.statsToken || this.statsInFlight) return;
        const token = this.statsToken;
        this.statsInFlight = true;
        try {
            const stats = await getMediaStats(token);
            if (token !== this.statsToken) return;
            this.syncActivity(stats);
        } catch (err) {
            console.warn('GetMediaStats failed:', err);
        } finally {
            this.statsInFlight = false;
        }
    }

    private syncActivity(stats: MediaStats | null): void {
        const text = stats ? this.activityLabel(stats) : '';
        const now = Date.now();
        if (text) {
            this.activityText = text;
            this.activityAt = now;
            this.scheduleActivityClear();
        } else if (this.activityText && now - this.activityAt >= STREAM_ACTIVITY_HOLD_MS) {
            this.activityText = '';
            this.clearActivityTimer();
        }
        this.renderMeta();
        this.refreshStatus();
    }

    private activityLabel(stats: MediaStats): string {
        const playback = stats.playback;
        if (playback.recentFloodWait) return RATE_LIMITED;
        const rate = playback.bytesPerSecond || 0;
        if (rate <= 0) return '';
        const multiplier = this.streamMultiplier(rate);
        return `${STREAMING_PREFIX}${formatStreamRate(rate)}${multiplier ? ` ${multiplier}` : ''}`;
    }

    private scheduleActivityClear(): void {
        this.clearActivityTimer();
        this.activityClearTimer = window.setTimeout(() => {
            if (Date.now() - this.activityAt >= STREAM_ACTIVITY_HOLD_MS) {
                this.activityText = '';
                this.renderMeta();
                this.refreshStatus();
            }
            this.activityClearTimer = null;
        }, STREAM_ACTIVITY_HOLD_MS);
    }

    private clearActivityTimer(): void {
        if (this.activityClearTimer == null) return;
        window.clearTimeout(this.activityClearTimer);
        this.activityClearTimer = null;
    }

    private clearActivity(): void {
        this.clearActivityTimer();
        this.activityText = '';
        this.activityAt = 0;
        this.renderMeta();
    }

    private renderMeta(): void {
        const { meta } = this.ctx.dom;
        if (!meta) return;
        meta.textContent = this.activityText ? `${this.metaBaseText} · ${this.activityText}` : this.metaBaseText;
    }

    private refreshStatus(): void {
        const { loadingStatus } = this.ctx.dom;
        if (!loadingStatus) return;
        loadingStatus.textContent = this.statusText();
    }

    private statusText(): string {
        if (this.statusOverride) return this.statusOverride;
        if (!this.ctx.hasAdapter()) return 'Opening video';
        if (this.activityText === RATE_LIMITED) return `Buffering · ${RATE_LIMITED}`;
        if (this.activityText.startsWith(STREAMING_PREFIX)) {
            return `Buffering · ${this.activityText.slice(STREAMING_PREFIX.length)}`;
        }
        return 'Buffering';
    }

    /**
     * How far ahead of realtime the stream is running, as a rough multiple of
     * the file's average bitrate. It is a reassurance indicator, not a
     * measurement, so it is capped rather than shown as an absurd number.
     */
    private streamMultiplier(bytesPerSecond: number): string {
        const duration = this.ctx.duration();
        if (!(this.metaBytes > 0 && duration > 0 && bytesPerSecond > 0)) return '';
        const averageBytesPerSecond = this.metaBytes / duration;
        if (!(averageBytesPerSecond > 0)) return '';
        const multiplier = bytesPerSecond / averageBytesPerSecond;
        if (!Number.isFinite(multiplier) || multiplier <= 0) return '';
        if (multiplier >= 100) return '(~99x+)';
        if (multiplier < 10) return `(~${Math.max(0.1, multiplier).toFixed(1)}x)`;
        return `(~${Math.round(multiplier)}x)`;
    }
}

function formatStreamRate(bytesPerSecond: number): string {
    const safe = Math.max(0, Number.isFinite(bytesPerSecond) ? bytesPerSecond : 0);
    if (safe < 1024 * 1024) {
        return `${Math.max(0.1, safe / 1024).toFixed(1)} KB/s`;
    }
    return `${(safe / (1024 * 1024)).toFixed(1)} MB/s`;
}
