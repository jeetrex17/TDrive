import {
    hideNativeSeekThumbnail,
    moveNativeSeekThumbnail,
    showNativeSeekThumbnail,
    type NativeMediaOpenResult,
    type NativeMediaRect,
} from '../../api';
import {
    coalesceBufferedRanges,
    type PlayerAdapter,
    type PlayerState,
} from './player-adapters';
import { nativeSeekOverlayAvailable } from './video-geometry';
import type { VideoDOM } from './video-dom';

interface VideoTransportContext {
    dom: VideoDOM;
    getAdapter(): PlayerAdapter | null;
    getState(): PlayerState;
    getNative(): NativeMediaOpenResult | null;
    hasError(): boolean;
    isNativeFallbackActive(): boolean;
    markPausedByUser(paused: boolean): void;
    revealChrome(): void;
    scheduleChromeHide(): void;
    geometryChanged(): void;
    refreshFullscreenAvailability(): void;
}

export const SEEK_STEP_SECONDS = 10;
export const VOLUME_STEP = 0.05;
const THUMBNAIL_BUCKET_SECONDS = 10;
const THUMBNAIL_LONG_BUCKET_SECONDS = 20;
const THUMBNAIL_VERY_LONG_BUCKET_SECONDS = 30;
const THUMBNAIL_REQUEST_DEBOUNCE_MS = 140;
const THUMBNAIL_DWELL_PREFETCH_MS = 420;
const THUMBNAIL_RETRY_MS = 650;
const THUMBNAIL_FAILURE_TTL_MS = 15_000;
const THUMBNAIL_NEAREST_MAX_SECONDS = 120;
const NATIVE_SEEK_PREVIEW_WIDTH = 144;
const NATIVE_SEEK_MOVE_THROTTLE_MS = 16;

function clamp(value: number, min: number, max: number): number {
    return Math.max(min, Math.min(max, Number.isFinite(value) ? value : min));
}

function percent(value: number, total: number): number {
    return total > 0 ? clamp((value / total) * 100, 0, 100) : 0;
}

function formatTime(value: number): string {
    if (!Number.isFinite(value) || value < 0) return '0:00';
    const total = Math.floor(value);
    const hours = Math.floor(total / 3600);
    const minutes = Math.floor((total % 3600) / 60);
    const seconds = total % 60;
    if (hours > 0) {
        return `${hours}:${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`;
    }
    return `${minutes}:${String(seconds).padStart(2, '0')}`;
}

function setSliderARIA(el: HTMLElement | null, value: number, min: number, max: number, text: string): void {
    if (!el) return;
    el.setAttribute('aria-valuemin', String(min));
    el.setAttribute('aria-valuemax', String(max));
    el.setAttribute('aria-valuenow', String(Math.round(value)));
    el.setAttribute('aria-valuetext', text);
}

function setButtonDisabled(button: HTMLButtonElement | null, disabled: boolean): void {
    if (!button) return;
    button.disabled = disabled;
    button.setAttribute('aria-disabled', disabled ? 'true' : 'false');
}

export class VideoTransportController {
    private showEndTime = false;
    private endTimeTimer: number | null = null;
    private skipFeedbackTimer: ReturnType<typeof setTimeout> | null = null;
    private lastBufferedSignature = '';
    private seekingWithPointer = false;
    private volumeDragging = false;
    private pendingVolumeValue: number | null = null;
    private volumeCommandFrame = 0;
    private activeThumbnailURL = '';
    private currentPreviewBucket = -1;
    private lastPreviewRatio = 0;
    private thumbnailRequestSeq = 0;
    private thumbnailRequestTimer: number | null = null;
    private thumbnailDwellTimer: number | null = null;
    private scheduledThumbnailBucket = -1;
    private readonly thumbnailObjectURLs = new Map<number, string>();
    private readonly thumbnailBase64 = new Map<number, string>();
    private nativeSeekAspect = 9 / 16;
    private readonly pendingThumbnails = new Set<number>();
    private readonly failedThumbnails = new Map<number, number>();
    private nativeSeekThrottleTimer: number | null = null;
    private nativeSeekPending: { token: string; bucket: number; image?: string; rect: NativeMediaRect } | null = null;
    private nativeSeekLastShown: { token: string; bucket: number } | null = null;

    constructor(private readonly context: VideoTransportContext) {}

    bind(): void {
        const { dom } = this.context;
        dom.timeDisplay?.addEventListener('click', () => this.toggleEndTime());
        dom.centerPlayButton?.addEventListener('click', () => this.togglePlayback());
        dom.centerSkipBackButton?.addEventListener('click', () => this.seekBy(-SEEK_STEP_SECONDS));
        dom.centerSkipForwardButton?.addEventListener('click', () => this.seekBy(SEEK_STEP_SECONDS));
        dom.skipBackButton?.addEventListener('click', () => this.seekBy(-SEEK_STEP_SECONDS));
        dom.skipForwardButton?.addEventListener('click', () => this.seekBy(SEEK_STEP_SECONDS));
        dom.playButton?.addEventListener('click', () => this.togglePlayback());
        this.bindScrubber();
        this.bindVolume();
    }

    sync(state: PlayerState): void {
        this.syncButtonState(state);
        this.syncTimeline(state);
        this.previewVolume(state.muted ? 0 : state.volume);
        this.syncTransportAvailability(state);
        this.syncCenterPlay(state);
    }

    beginSession(thumbnailURL: string): void {
        this.activeThumbnailURL = thumbnailURL;
        this.thumbnailRequestSeq += 1;
    }

    resetSession(): void {
        this.resetEndTime();
        this.clearVolumeCommandFrame();
        this.clearSkipFeedback();
        this.resetThumbnailPreview();
    }

    isScrubberTooltipActive(): boolean {
        return Boolean(
            this.context.dom.scrubber?.classList.contains('is-hovered')
            || this.currentPreviewBucket >= 0
        );
    }

    togglePlayback(): void {
        const adapter = this.context.getAdapter();
        if (!adapter || this.context.hasError()) return;
        this.context.markPausedByUser(!this.context.getState().paused);
        adapter.playPause();
        this.context.revealChrome();
    }

    seekBy(delta: number): void {
        const adapter = this.context.getAdapter();
        const state = this.context.getState();
        if (!adapter || (state.duration <= 0 && !this.context.getNative())) return;
        adapter.seekRelative(delta);
        this.showSkipFeedback(delta);
        this.context.revealChrome();
    }

    private syncTransportAvailability(state: PlayerState): void {
        const { dom } = this.context;
        const fallback = this.context.isNativeFallbackActive();
        const canScrub = Boolean(this.context.getAdapter() && (state.duration > 0 || fallback));
        const canRelativeSeek = canScrub || fallback;
        setButtonDisabled(dom.skipBackButton, !canRelativeSeek);
        setButtonDisabled(dom.skipForwardButton, !canRelativeSeek);
        setButtonDisabled(dom.centerSkipBackButton, !canRelativeSeek);
        setButtonDisabled(dom.centerSkipForwardButton, !canRelativeSeek);
        if (dom.scrubber) {
            dom.scrubber.classList.toggle('is-disabled', !canScrub);
            dom.scrubber.setAttribute('aria-disabled', canScrub ? 'false' : 'true');
            dom.scrubber.tabIndex = canScrub ? 0 : -1;
        }
        this.context.refreshFullscreenAvailability();
    }

    private syncCenterPlay(state: PlayerState): void {
        const visible = Boolean(this.context.getAdapter() && state.paused && !state.loading && !this.context.hasError());
        this.context.dom.modal?.classList.toggle('is-video-paused', visible);
        this.context.dom.centerControls?.setAttribute('aria-hidden', visible ? 'false' : 'true');
    }

    private syncButtonState(state: PlayerState): void {
        const { playButton, muteButton } = this.context.dom;
        if (!playButton || !muteButton) return;
        playButton.dataset.state = state.paused ? 'paused' : 'playing';
        playButton.setAttribute('aria-label', state.paused ? 'Play' : 'Pause');
        playButton.title = state.paused ? 'Play' : 'Pause';
        muteButton.dataset.state = state.muted ? 'muted' : 'unmuted';
        muteButton.setAttribute('aria-label', state.muted ? 'Unmute' : 'Mute');
        muteButton.title = state.muted ? 'Unmute' : 'Mute';
    }

    private syncEndTime(state: PlayerState): void {
        const { timeDisplay, endTime } = this.context.dom;
        timeDisplay?.setAttribute('aria-pressed', String(this.showEndTime));
        if (timeDisplay) {
            timeDisplay.title = this.showEndTime
                ? `Hide estimated finish time${state.paused ? ' (if you resume now)' : ''}`
                : 'Show estimated finish time';
            timeDisplay.setAttribute('aria-label', timeDisplay.title);
            timeDisplay.setAttribute(
                'aria-describedby',
                this.showEndTime ? 'video-time video-duration video-end-time' : 'video-time video-duration',
            );
        }
        if (!endTime) return;
        endTime.classList.toggle('is-visible', this.showEndTime);
        endTime.setAttribute('aria-hidden', String(!this.showEndTime));
        if (!this.showEndTime) return;
        const remaining = Math.max(0, state.duration - state.currentTime) / state.rate;
        const finish = new Date(Date.now() + remaining * 1000);
        const valid = Number.isFinite(state.duration) && state.duration > 0
            && Number.isFinite(state.currentTime) && Number.isFinite(state.rate) && state.rate > 0
            && Number.isFinite(finish.getTime());
        const endTimeText = endTime.firstElementChild;
        if (!endTimeText) return;
        endTimeText.textContent = valid
            ? ` · Ends at ${new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit' }).format(finish)}`
            : ' · End time unavailable';
    }

    private resetEndTime(): void {
        if (this.endTimeTimer !== null) window.clearInterval(this.endTimeTimer);
        this.endTimeTimer = null;
        this.showEndTime = false;
        this.syncEndTime(this.context.getState());
    }

    private toggleEndTime(): void {
        if (this.showEndTime) {
            this.resetEndTime();
        } else {
            this.showEndTime = true;
            this.syncEndTime(this.context.getState());
            // Paused playback emits no time updates; the estimate assumes
            // resuming now.
            this.endTimeTimer = window.setInterval(() => {
                this.syncEndTime(this.context.getState());
            }, 1_000);
        }
        this.context.geometryChanged();
        this.context.revealChrome();
    }

    private syncTimeline(state: PlayerState): void {
        const { dom } = this.context;
        this.syncEndTime(state);
        if (dom.time) dom.time.textContent = formatTime(state.currentTime);
        if (dom.duration) dom.duration.textContent = state.duration > 0 ? formatTime(state.duration) : '--:--';

        const played = percent(state.currentTime, state.duration);
        if (dom.scrubberPlayed && !this.seekingWithPointer) dom.scrubberPlayed.style.width = `${played}%`;
        if (dom.scrubberThumb && !this.seekingWithPointer) dom.scrubberThumb.style.left = `${played}%`;
        this.renderBuffered(state);
        setSliderARIA(
            dom.scrubber,
            state.currentTime,
            0,
            Math.max(0, state.duration),
            `${formatTime(state.currentTime)} of ${state.duration > 0 ? formatTime(state.duration) : 'unknown'}`,
        );
    }

    private renderBuffered(state: PlayerState): void {
        const container = this.context.dom.scrubberBuffered;
        if (!container) return;

        const segments: Array<{ left: number; width: number }> = [];
        if (state.duration > 0) {
            for (const range of coalesceBufferedRanges(state.buffered, state.duration)) {
                const left = clamp((range.start / state.duration) * 100, 0, 100);
                const width = clamp(((range.end - range.start) / state.duration) * 100, 0, 100 - left);
                if (width > 0) segments.push({ left, width });
            }
        }

        const signature = segments.map((segment) => (
            `${segment.left.toFixed(3)}:${segment.width.toFixed(3)}`
        )).join('|');
        if (signature === this.lastBufferedSignature) return;
        this.lastBufferedSignature = signature;

        while (container.childElementCount > segments.length) container.lastElementChild?.remove();
        while (container.childElementCount < segments.length) {
            const segment = document.createElement('span');
            segment.className = 'video-scrubber-segment';
            container.appendChild(segment);
        }
        segments.forEach((segment, index) => {
            const node = container.children[index] as HTMLElement;
            node.style.left = `${segment.left}%`;
            node.style.width = `${segment.width}%`;
        });
    }

    private previewVolume(value: number): void {
        const safe = clamp(value, 0, 1);
        const { volumeFill, volumeThumb, volumeSlider } = this.context.dom;
        if (volumeFill) volumeFill.style.width = `${safe * 100}%`;
        if (volumeThumb) volumeThumb.style.left = `${safe * 100}%`;
        setSliderARIA(volumeSlider, safe * 100, 0, 100, `${Math.round(safe * 100)}%`);
    }

    private showSkipFeedback(delta: number): void {
        const feedback = this.context.dom.skipFeedback;
        if (!feedback) return;
        const value = Math.abs(Math.round(delta));
        const text = feedback.querySelector('span');
        if (text) text.textContent = `${delta > 0 ? '+' : '-'}${value}s`;

        if (this.skipFeedbackTimer) clearTimeout(this.skipFeedbackTimer);
        feedback.classList.remove('is-visible', 'is-forward', 'is-back');
        // Force style resolution so repeated skips restart the pulse.
        void feedback.offsetWidth;
        feedback.classList.add(delta > 0 ? 'is-forward' : 'is-back', 'is-visible');
        this.skipFeedbackTimer = setTimeout(() => {
            this.skipFeedbackTimer = null;
            feedback.classList.remove('is-visible');
        }, 620);
    }

    private clearSkipFeedback(): void {
        if (this.skipFeedbackTimer) clearTimeout(this.skipFeedbackTimer);
        this.skipFeedbackTimer = null;
        this.context.dom.skipFeedback?.classList.remove('is-visible', 'is-forward', 'is-back');
    }

    private bindScrubber(): void {
        const scrubber = this.context.dom.scrubber;
        scrubber?.addEventListener('pointerenter', (event) => {
            scrubber.classList.add('is-hovered');
            this.previewScrubber(event);
        });
        scrubber?.addEventListener('pointermove', (event) => {
            this.previewScrubber(event);
            if (this.seekingWithPointer) this.updateScrubVisual(this.scrubberSecondsFromEvent(event));
        });
        scrubber?.addEventListener('pointerleave', () => {
            this.currentPreviewBucket = -1;
            this.clearThumbnailDwellTimer();
            this.hideNativeSeekPreview();
            if (!this.seekingWithPointer) scrubber.classList.remove('is-hovered');
            this.context.scheduleChromeHide();
        });
        scrubber?.addEventListener('pointerdown', (event) => {
            if (!this.context.getAdapter() || this.context.getState().duration <= 0) return;
            this.seekingWithPointer = true;
            scrubber.setPointerCapture(event.pointerId);
            scrubber.classList.add('is-dragging', 'is-hovered');
            this.updateScrubVisual(this.scrubberSecondsFromEvent(event));
            this.context.revealChrome();
        });
        scrubber?.addEventListener('pointerup', (event) => {
            const adapter = this.context.getAdapter();
            if (!adapter || this.context.getState().duration <= 0) return;
            const seconds = this.scrubberSecondsFromEvent(event);
            this.seekingWithPointer = false;
            if (scrubber.hasPointerCapture(event.pointerId)) scrubber.releasePointerCapture(event.pointerId);
            scrubber.classList.remove('is-dragging');
            adapter.seekAbsolute(seconds);
            this.context.revealChrome();
        });
        scrubber?.addEventListener('pointercancel', (event) => {
            this.seekingWithPointer = false;
            if (scrubber.hasPointerCapture(event.pointerId)) scrubber.releasePointerCapture(event.pointerId);
            scrubber.classList.remove('is-dragging', 'is-hovered');
            this.syncTimeline(this.context.getState());
        });
        scrubber?.addEventListener('keydown', (event) => {
            if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
            event.preventDefault();
            event.stopPropagation();
            const adapter = this.context.getAdapter();
            const state = this.context.getState();
            if (!adapter || state.duration <= 0) return;
            if (event.key === 'ArrowLeft') adapter.seekRelative(-SEEK_STEP_SECONDS);
            else if (event.key === 'ArrowRight') adapter.seekRelative(SEEK_STEP_SECONDS);
            else if (event.key === 'Home') adapter.seekAbsolute(0);
            else adapter.seekAbsolute(state.duration);
        });
    }

    private bindVolume(): void {
        const { muteButton, volumeSlider } = this.context.dom;
        muteButton?.addEventListener('click', () => {
            const adapter = this.context.getAdapter();
            if (adapter) adapter.setMuted(!this.context.getState().muted);
            this.context.revealChrome();
        });
        volumeSlider?.addEventListener('pointerdown', (event) => {
            volumeSlider.focus({ preventScroll: true });
            if (!this.context.getAdapter()) return;
            this.volumeDragging = true;
            volumeSlider.setPointerCapture(event.pointerId);
            this.setVolumeFromPointer(event);
        });
        volumeSlider?.addEventListener('pointermove', (event) => {
            if (this.volumeDragging) this.setVolumeFromPointer(event);
        });
        volumeSlider?.addEventListener('pointerup', (event) => {
            if (!this.volumeDragging) return;
            this.volumeDragging = false;
            if (volumeSlider.hasPointerCapture(event.pointerId)) volumeSlider.releasePointerCapture(event.pointerId);
            this.setVolumeFromPointer(event);
        });
        volumeSlider?.addEventListener('pointercancel', (event) => {
            this.volumeDragging = false;
            if (volumeSlider.hasPointerCapture(event.pointerId)) volumeSlider.releasePointerCapture(event.pointerId);
        });
        volumeSlider?.addEventListener('keydown', (event) => {
            if (!['ArrowLeft', 'ArrowDown', 'ArrowRight', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
            event.preventDefault();
            event.stopPropagation();
            const adapter = this.context.getAdapter();
            if (!adapter) return;
            const volume = this.context.getState().volume;
            if (event.key === 'ArrowLeft' || event.key === 'ArrowDown') adapter.setVolume(volume - VOLUME_STEP);
            else if (event.key === 'ArrowRight' || event.key === 'ArrowUp') adapter.setVolume(volume + VOLUME_STEP);
            else if (event.key === 'Home') adapter.setVolume(0);
            else adapter.setVolume(1);
        });
    }

    private scrubberSecondsFromEvent(event: PointerEvent | MouseEvent): number {
        const scrubber = this.context.dom.scrubber;
        const duration = this.context.getState().duration;
        if (!scrubber || duration <= 0) return 0;
        const rect = scrubber.getBoundingClientRect();
        const ratio = clamp((event.clientX - rect.left) / Math.max(1, rect.width), 0, 1);
        return ratio * duration;
    }

    private previewScrubber(event: PointerEvent | MouseEvent): void {
        const { scrubber, scrubberTooltip, scrubberTooltipTime } = this.context.dom;
        if (!scrubber || !scrubberTooltip) return;
        const rect = scrubber.getBoundingClientRect();
        const ratio = clamp((event.clientX - rect.left) / Math.max(1, rect.width), 0, 1);
        this.lastPreviewRatio = ratio;
        if (this.context.getState().duration <= 0) {
            if (this.context.isNativeFallbackActive() && this.activeThumbnailURL) {
                this.currentPreviewBucket = -1;
                if (scrubberTooltipTime) scrubberTooltipTime.textContent = '--:--';
                this.setThumbnailTooltipState('pending');
                this.positionScrubberTooltip(ratio, rect);
            }
            return;
        }
        const seconds = ratio * this.context.getState().duration;
        this.updateThumbnailTooltip(seconds);
        this.positionScrubberTooltip(ratio, rect);
    }

    private positionScrubberTooltip(ratio: number, rect?: DOMRect): void {
        const { scrubber, scrubberTooltip } = this.context.dom;
        if (!scrubber || !scrubberTooltip) return;
        const bounds = rect || scrubber.getBoundingClientRect();
        const tooltipWidth = scrubberTooltip.offsetWidth || 44;
        const half = tooltipWidth / 2;
        const x = clamp(ratio * bounds.width, half, Math.max(half, bounds.width - half));
        scrubberTooltip.style.left = `${x}px`;
    }

    private updateThumbnailTooltip(seconds: number): void {
        const { scrubberTooltipImage, scrubberTooltip, scrubberTooltipTime } = this.context.dom;
        if (scrubberTooltipTime) scrubberTooltipTime.textContent = formatTime(seconds);
        const bucket = this.thumbnailBucket(seconds);
        this.currentPreviewBucket = bucket;
        const cached = this.thumbnailObjectURLs.get(bucket);
        if (cached && scrubberTooltipImage && scrubberTooltip) {
            this.clearThumbnailRequestTimer();
            this.scheduleThumbnailDwell(bucket);
            this.showTooltipImage(cached);
            this.setThumbnailTooltipState('ready');
            this.presentNativeSeekPreview(bucket);
            return;
        }
        this.clearThumbnailDwellTimer();
        const nearestBucket = this.nearestCachedBucket(bucket);
        if (nearestBucket !== null && scrubberTooltipImage && scrubberTooltip) {
            const nearestURL = this.thumbnailObjectURLs.get(nearestBucket);
            if (nearestURL) this.showTooltipImage(nearestURL);
            this.setThumbnailTooltipState('ready');
            this.presentNativeSeekPreview(nearestBucket);
            this.scheduleThumbnailRequest(bucket, true);
            return;
        }
        if (this.thumbnailFailedRecently(bucket)) {
            this.setThumbnailTooltipState('failed');
            return;
        }
        this.setThumbnailTooltipState('pending');
        this.scheduleThumbnailRequest(bucket);
    }

    private nearestCachedBucket(bucket: number): number | null {
        let best: number | null = null;
        let bestDistance = Infinity;
        for (const cachedBucket of this.thumbnailObjectURLs.keys()) {
            const distance = Math.abs(cachedBucket - bucket);
            if (distance <= THUMBNAIL_NEAREST_MAX_SECONDS && distance < bestDistance) {
                bestDistance = distance;
                best = cachedBucket;
            }
        }
        return best;
    }

    private showTooltipImage(url: string): void {
        const image = this.context.dom.scrubberTooltipImage;
        if (image && image.src !== url) image.src = url;
    }

    private presentNativeSeekPreview(bucket: number): void {
        if (!this.context.isNativeFallbackActive() || !nativeSeekOverlayAvailable()) return;
        const token = this.context.getNative()?.token;
        if (!token) return;
        const cached = this.thumbnailBase64.get(bucket);
        if (cached) {
            const rect = this.nativeSeekOverlayRect();
            if (rect) this.queueNativeSeek(token, bucket, cached, rect);
            return;
        }
        const url = this.thumbnailObjectURLs.get(bucket);
        if (!url) return;
        void this.objectURLToBase64(url).then((image) => {
            if (!image) return;
            this.thumbnailBase64.set(bucket, image);
            if (
                this.currentPreviewBucket === bucket
                && this.context.isNativeFallbackActive()
                && nativeSeekOverlayAvailable()
                && this.context.getNative()?.token === token
            ) {
                const rect = this.nativeSeekOverlayRect();
                if (rect) this.queueNativeSeek(token, bucket, image, rect);
            }
        });
    }

    private queueNativeSeek(token: string, bucket: number, image: string, rect: NativeMediaRect): void {
        const needsUpload = !this.nativeSeekLastShown
            || this.nativeSeekLastShown.token !== token
            || this.nativeSeekLastShown.bucket !== bucket;
        this.nativeSeekPending = { token, bucket, image: needsUpload ? image : undefined, rect };
        if (this.nativeSeekThrottleTimer !== null) return;
        this.flushNativeSeek();
        this.nativeSeekThrottleTimer = window.setTimeout(() => {
            this.nativeSeekThrottleTimer = null;
            this.flushNativeSeek();
        }, NATIVE_SEEK_MOVE_THROTTLE_MS);
    }

    private flushNativeSeek(): void {
        const request = this.nativeSeekPending;
        this.nativeSeekPending = null;
        if (!request) return;
        if (request.image) {
            this.nativeSeekLastShown = { token: request.token, bucket: request.bucket };
            void showNativeSeekThumbnail(request.token, request.image, request.rect);
        } else {
            void moveNativeSeekThumbnail(request.token, request.rect);
        }
    }

    private hideNativeSeekPreview(): void {
        this.nativeSeekPending = null;
        this.nativeSeekLastShown = null;
        if (this.nativeSeekThrottleTimer !== null) window.clearTimeout(this.nativeSeekThrottleTimer);
        this.nativeSeekThrottleTimer = null;
        const token = this.context.getNative()?.token;
        if (token && nativeSeekOverlayAvailable()) void hideNativeSeekThumbnail(token);
    }

    private nativeSeekOverlayRect(): NativeMediaRect | null {
        const { scrubber, scrubberTooltipImage, nativeViewport } = this.context.dom;
        if (!scrubber) return null;
        const bounds = scrubber.getBoundingClientRect();
        if (bounds.width < 2) return null;
        if (scrubberTooltipImage && scrubberTooltipImage.naturalWidth > 0 && scrubberTooltipImage.naturalHeight > 0) {
            this.nativeSeekAspect = scrubberTooltipImage.naturalHeight / scrubberTooltipImage.naturalWidth;
        }
        const width = NATIVE_SEEK_PREVIEW_WIDTH;
        const height = Math.max(1, Math.round(width * this.nativeSeekAspect));
        const gap = 8;
        const viewport = nativeViewport?.getBoundingClientRect();
        const leftBound = (viewport?.left ?? bounds.left) + gap;
        const rightBound = (viewport?.right ?? bounds.right) - gap;
        const topBound = (viewport?.top ?? 0) + gap;
        const bottomBound = (viewport?.bottom ?? bounds.top) - gap;
        const centerX = bounds.left + clamp(this.lastPreviewRatio, 0, 1) * bounds.width;
        const x = clamp(centerX - width / 2, leftBound, Math.max(leftBound, rightBound - width));
        const y = clamp(bounds.top - height - gap, topBound, Math.max(topBound, bottomBound - height));
        return { x, y, width, height };
    }

    private async blobToBase64(blob: Blob): Promise<string | null> {
        try {
            const bytes = new Uint8Array(await blob.arrayBuffer());
            let binary = '';
            const chunk = 0x8000;
            for (let i = 0; i < bytes.length; i += chunk) {
                binary += String.fromCharCode(...bytes.subarray(i, i + chunk));
            }
            return btoa(binary);
        } catch {
            return null;
        }
    }

    private async objectURLToBase64(url: string): Promise<string | null> {
        try {
            const response = await fetch(url);
            return await this.blobToBase64(await response.blob());
        } catch {
            return null;
        }
    }

    private thumbnailBucket(seconds: number): number {
        if (!Number.isFinite(seconds) || seconds < 0) return 0;
        const interval = this.thumbnailBucketInterval(this.context.getState().duration);
        return Math.max(0, Math.round(seconds / interval) * interval);
    }

    private thumbnailBucketInterval(duration: number): number {
        if (duration >= 2 * 60 * 60) return THUMBNAIL_VERY_LONG_BUCKET_SECONDS;
        if (duration >= 30 * 60) return THUMBNAIL_LONG_BUCKET_SECONDS;
        return THUMBNAIL_BUCKET_SECONDS;
    }

    private clearThumbnailRequestTimer(): void {
        if (this.thumbnailRequestTimer == null) return;
        window.clearTimeout(this.thumbnailRequestTimer);
        this.thumbnailRequestTimer = null;
        this.scheduledThumbnailBucket = -1;
    }

    private clearThumbnailDwellTimer(): void {
        if (this.thumbnailDwellTimer == null) return;
        window.clearTimeout(this.thumbnailDwellTimer);
        this.thumbnailDwellTimer = null;
    }

    private scheduleThumbnailRequest(bucket: number, keepVisible = false): void {
        if (!this.activeThumbnailURL || this.thumbnailObjectURLs.has(bucket)) return;
        this.scheduledThumbnailBucket = bucket;
        if (this.thumbnailRequestTimer != null) window.clearTimeout(this.thumbnailRequestTimer);
        this.thumbnailRequestTimer = window.setTimeout(() => {
            this.thumbnailRequestTimer = null;
            const bucketToRequest = this.scheduledThumbnailBucket;
            this.scheduledThumbnailBucket = -1;
            if (bucketToRequest !== this.currentPreviewBucket) return;
            this.requestThumbnail(bucketToRequest, false, keepVisible);
        }, THUMBNAIL_REQUEST_DEBOUNCE_MS);
    }

    private scheduleThumbnailDwell(bucket: number): void {
        this.clearThumbnailDwellTimer();
        if (!this.activeThumbnailURL || !this.thumbnailObjectURLs.has(bucket) || this.seekingWithPointer) return;
        this.thumbnailDwellTimer = window.setTimeout(() => {
            this.thumbnailDwellTimer = null;
            if (this.currentPreviewBucket !== bucket || this.seekingWithPointer || !this.thumbnailObjectURLs.has(bucket)) return;
            const duration = this.context.getState().duration;
            const interval = this.thumbnailBucketInterval(duration);
            for (const neighbor of [bucket - interval, bucket + interval, bucket - 2 * interval, bucket + 2 * interval]) {
                if (neighbor < 0 || neighbor > duration) continue;
                this.requestThumbnail(neighbor, true);
            }
        }, THUMBNAIL_DWELL_PREFETCH_MS);
    }

    private requestThumbnail(bucket: number, prefetch = false, keepVisible = false): void {
        if (!this.activeThumbnailURL || this.pendingThumbnails.has(bucket) || this.thumbnailObjectURLs.has(bucket)) return;
        if (this.thumbnailFailedRecently(bucket)) return;
        this.pendingThumbnails.add(bucket);
        if (!prefetch && !keepVisible && this.currentPreviewBucket === bucket) this.setThumbnailTooltipState('pending');
        const sequence = this.thumbnailRequestSeq;
        const url = `${this.activeThumbnailURL}?t=${encodeURIComponent(String(bucket))}`;
        let retryScheduled = false;
        fetch(url, { cache: 'no-store' })
            .then(async (response) => {
                if (sequence !== this.thumbnailRequestSeq) return;
                if (response.status === 202) {
                    retryScheduled = true;
                    window.setTimeout(() => {
                        this.pendingThumbnails.delete(bucket);
                        if (sequence === this.thumbnailRequestSeq && this.currentPreviewBucket === bucket) {
                            this.requestThumbnail(bucket);
                        }
                    }, THUMBNAIL_RETRY_MS);
                    return;
                }
                if (!response.ok) {
                    this.failedThumbnails.set(bucket, Date.now());
                    if (!prefetch && !keepVisible && this.currentPreviewBucket === bucket) this.setThumbnailTooltipState('failed');
                    return;
                }
                const blob = await response.blob();
                if (!blob.size || sequence !== this.thumbnailRequestSeq) return;
                const nativeImage = this.context.isNativeFallbackActive() ? await this.blobToBase64(blob) : null;
                if (sequence !== this.thumbnailRequestSeq) return;
                const objectURL = URL.createObjectURL(blob);
                const old = this.thumbnailObjectURLs.get(bucket);
                if (old) URL.revokeObjectURL(old);
                this.thumbnailObjectURLs.set(bucket, objectURL);
                if (nativeImage) this.thumbnailBase64.set(bucket, nativeImage);
                this.failedThumbnails.delete(bucket);
                const { scrubberTooltipImage, scrubberTooltip } = this.context.dom;
                if (this.currentPreviewBucket === bucket && scrubberTooltipImage && scrubberTooltip) {
                    scrubberTooltipImage.src = objectURL;
                    this.setThumbnailTooltipState('ready');
                    this.positionScrubberTooltip(this.lastPreviewRatio);
                    this.scheduleThumbnailDwell(bucket);
                    this.presentNativeSeekPreview(bucket);
                }
            })
            .catch(() => {
                if (sequence === this.thumbnailRequestSeq) this.failedThumbnails.set(bucket, Date.now());
                if (!prefetch && !keepVisible && sequence === this.thumbnailRequestSeq && this.currentPreviewBucket === bucket) {
                    this.setThumbnailTooltipState('failed');
                }
            })
            .finally(() => {
                if (sequence === this.thumbnailRequestSeq && !retryScheduled) this.pendingThumbnails.delete(bucket);
            });
    }

    private thumbnailFailedRecently(bucket: number): boolean {
        const failedAt = this.failedThumbnails.get(bucket);
        return Boolean(failedAt && Date.now() - failedAt < THUMBNAIL_FAILURE_TTL_MS);
    }

    private setThumbnailTooltipState(state: 'pending' | 'ready' | 'failed'): void {
        const { scrubberTooltip, scrubberTooltipImage } = this.context.dom;
        if (!scrubberTooltip) return;
        scrubberTooltip.classList.toggle('has-thumbnail', state === 'ready');
        scrubberTooltip.classList.toggle('is-thumbnail-pending', state === 'pending');
        scrubberTooltip.classList.toggle('is-thumbnail-failed', state === 'failed');
        if (state !== 'ready') scrubberTooltipImage?.removeAttribute('src');
    }

    private resetThumbnailPreview(): void {
        this.thumbnailRequestSeq += 1;
        this.activeThumbnailURL = '';
        this.currentPreviewBucket = -1;
        this.lastPreviewRatio = 0;
        this.clearThumbnailRequestTimer();
        this.clearThumbnailDwellTimer();
        this.pendingThumbnails.clear();
        this.failedThumbnails.clear();
        for (const objectURL of this.thumbnailObjectURLs.values()) URL.revokeObjectURL(objectURL);
        this.thumbnailObjectURLs.clear();
        this.thumbnailBase64.clear();
        this.nativeSeekAspect = 9 / 16;
        this.hideNativeSeekPreview();
        const { scrubberTooltipImage, scrubberTooltipTime, scrubberTooltip } = this.context.dom;
        scrubberTooltipImage?.removeAttribute('src');
        if (scrubberTooltipTime) scrubberTooltipTime.textContent = '0:00';
        scrubberTooltip?.classList.remove('has-thumbnail', 'is-thumbnail-pending', 'is-thumbnail-failed');
    }

    private updateScrubVisual(seconds: number): void {
        const played = percent(seconds, this.context.getState().duration);
        const { scrubberPlayed, scrubberThumb, time } = this.context.dom;
        if (scrubberPlayed) scrubberPlayed.style.width = `${played}%`;
        if (scrubberThumb) scrubberThumb.style.left = `${played}%`;
        if (time) time.textContent = formatTime(seconds);
    }

    private volumeFromEvent(event: PointerEvent | MouseEvent): number {
        const slider = this.context.dom.volumeSlider;
        if (!slider) return this.context.getState().volume;
        const rect = slider.getBoundingClientRect();
        return clamp((event.clientX - rect.left) / Math.max(1, rect.width), 0, 1);
    }

    private setVolumeFromPointer(event: PointerEvent | MouseEvent): void {
        const value = this.volumeFromEvent(event);
        this.previewVolume(value);
        this.scheduleVolumeSet(value);
        this.context.revealChrome();
    }

    private scheduleVolumeSet(value: number): void {
        this.pendingVolumeValue = clamp(value, 0, 1);
        if (this.volumeCommandFrame) return;
        this.volumeCommandFrame = requestAnimationFrame(() => {
            this.volumeCommandFrame = 0;
            const next = this.pendingVolumeValue;
            this.pendingVolumeValue = null;
            if (next != null) this.context.getAdapter()?.setVolume(next);
        });
    }

    private clearVolumeCommandFrame(): void {
        if (this.volumeCommandFrame) cancelAnimationFrame(this.volumeCommandFrame);
        this.volumeCommandFrame = 0;
        this.pendingVolumeValue = null;
    }
}
