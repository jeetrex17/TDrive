import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { NativeMediaOpenResult } from '../../api';
import { EMPTY_PLAYER_STATE, type PlayerAdapter, type PlayerState } from './player-adapters';
import { SEEK_STEP_SECONDS, VideoTransportController, VOLUME_STEP } from './video-transport';
import type { VideoDOM } from './video-dom';

const apiMock = vi.hoisted(() => ({
    hideNativeSeekThumbnail: vi.fn(),
    moveNativeSeekThumbnail: vi.fn(),
    showNativeSeekThumbnail: vi.fn(),
}));

const nativeSeekOverlayAvailableMock = vi.hoisted(() => vi.fn(() => false));

vi.mock('../../api', () => ({
    ...apiMock,
}));

vi.mock('./video-geometry', () => ({
    nativeSeekOverlayAvailable: nativeSeekOverlayAvailableMock,
}));

function button(id: string): HTMLButtonElement {
    const el = document.createElement('button');
    el.id = id;
    return el;
}

function div(id: string): HTMLElement {
    const el = document.createElement('div');
    el.id = id;
    return el;
}

function span(id: string): HTMLElement {
    const el = document.createElement('span');
    el.id = id;
    return el;
}

function mountDOM(): VideoDOM {
    const modal = div('video-modal');
    const centerControls = div('video-center-controls');
    const scrubber = div('video-scrubber');
    const volumeSlider = div('video-volume-slider');
    const skipFeedback = div('video-skip-feedback');
    const skipFeedbackText = document.createElement('span');
    const endTime = span('video-end-time');
    const endTimeText = document.createElement('span');
    const scrubberTooltip = div('video-scrubber-tooltip');
    const scrubberTooltipImage = document.createElement('img');
    const scrubberTooltipTime = span('video-scrubber-tooltip-time');
    const scrubberBuffered = div('video-scrubber-buffered');
    const scrubberPlayed = div('video-scrubber-played');
    const scrubberThumb = div('video-scrubber-thumb');
    const volumeFill = div('video-volume-fill');
    const volumeThumb = div('video-volume-thumb');

    skipFeedback.append(skipFeedbackText);
    endTime.append(endTimeText);
    scrubberTooltip.append(scrubberTooltipImage, scrubberTooltipTime);
    modal.append(centerControls, scrubber, volumeSlider, skipFeedback, endTime, scrubberTooltip);
    document.body.append(modal);

    installRect(scrubber, { x: 10, y: 100, width: 200, height: 20 });
    installRect(volumeSlider, { x: 20, y: 0, width: 100, height: 20 });

    return {
        modal,
        stage: div('stage'),
        topbar: div('topbar'),
        controls: div('controls'),
        filename: span('filename'),
        meta: span('meta'),
        closeButton: button('close'),
        nativeViewport: div('native-viewport'),
        standalone: div('standalone'),
        video: document.createElement('video'),
        loading: div('loading'),
        loadingStatus: div('loading-status'),
        error: div('error'),
        errorMessage: div('error-message'),
        errorRetryButton: button('error-retry'),
        errorCloseButton: button('error-close'),
        centerControls,
        centerPlayButton: button('center-play'),
        centerSkipBackButton: button('center-skip-back'),
        centerSkipForwardButton: button('center-skip-forward'),
        skipFeedback,
        playButton: button('play'),
        skipBackButton: button('skip-back'),
        skipForwardButton: button('skip-forward'),
        muteButton: button('mute'),
        fullscreenButton: button('fullscreen'),
        scrubber,
        scrubberPlayed,
        scrubberBuffered,
        scrubberThumb,
        scrubberTooltip,
        scrubberTooltipImage,
        scrubberTooltipTime,
        volumeSlider,
        volumeFill,
        volumeThumb,
        time: span('time'),
        duration: span('duration'),
        timeDisplay: button('time-display'),
        endTime,
        speedButton: button('speed'),
        speedMenu: div('speed-menu'),
        playlistButton: button('playlist'),
        playlistPanel: div('playlist-panel'),
        audioPicker: { wrap: null, button: null, label: null, menu: null },
        subtitlePicker: { wrap: null, button: null, label: null, menu: null },
    };
}

function installRect(el: HTMLElement, rect: DOMRectInit): void {
    const domRect = DOMRect.fromRect({
        x: rect.x ?? 0,
        y: rect.y ?? 0,
        width: rect.width,
        height: rect.height,
    });
    vi.spyOn(el, 'getBoundingClientRect').mockReturnValue(domRect);
    Object.defineProperty(el, 'offsetWidth', { configurable: true, value: rect.width ?? 0 });
}

function pointerEvent(type: string, init: { clientX: number; pointerId?: number; isPrimary?: boolean }): Event {
    const event = new Event(type, { bubbles: true });
    Object.defineProperties(event, {
        clientX: { value: init.clientX },
        pointerId: { value: init.pointerId ?? 1 },
        isPrimary: { value: init.isPrimary ?? true },
    });
    return event;
}

function keyEvent(key: string): KeyboardEvent {
    const event = new Event('keydown', { bubbles: true });
    Object.defineProperty(event, 'key', { value: key });
    Object.defineProperty(event, 'preventDefault', { value: vi.fn() });
    Object.defineProperty(event, 'stopPropagation', { value: vi.fn() });
    return event as KeyboardEvent;
}

function makeAdapter(): PlayerAdapter {
    return {
        subscribe: vi.fn(() => () => {}),
        playPause: vi.fn(),
        seekAbsolute: vi.fn(),
        seekRelative: vi.fn(),
        setVolume: vi.fn(),
        setMuted: vi.fn(),
        setSpeed: vi.fn(),
        close: vi.fn(async () => {}),
    };
}

function makeHarness(overrides: Partial<PlayerState> = {}) {
    const dom = mountDOM();
    const adapter = makeAdapter();
    let activeAdapter: PlayerAdapter | null = adapter;
    let state: PlayerState = { ...EMPTY_PLAYER_STATE, duration: 120, currentTime: 30, volume: 0.4, ...overrides };
    let native: NativeMediaOpenResult | null = null;
    let hasError = false;
    let nativeFallback = false;
    const markPausedByUser = vi.fn();
    const revealChrome = vi.fn();
    const scheduleChromeHide = vi.fn();
    const geometryChanged = vi.fn();
    const refreshFullscreenAvailability = vi.fn();

    dom.scrubber!.setPointerCapture = vi.fn();
    dom.scrubber!.releasePointerCapture = vi.fn();
    dom.scrubber!.hasPointerCapture = vi.fn(() => true);
    dom.volumeSlider!.setPointerCapture = vi.fn();
    dom.volumeSlider!.releasePointerCapture = vi.fn();
    dom.volumeSlider!.hasPointerCapture = vi.fn(() => true);

    const controller = new VideoTransportController({
        dom,
        getAdapter: () => activeAdapter,
        getState: () => state,
        getNative: () => native,
        hasError: () => hasError,
        isNativeFallbackActive: () => nativeFallback,
        markPausedByUser,
        revealChrome,
        scheduleChromeHide,
        geometryChanged,
        refreshFullscreenAvailability,
    });

    return {
        adapter,
        controller,
        dom,
        geometryChanged,
        markPausedByUser,
        refreshFullscreenAvailability,
        revealChrome,
        scheduleChromeHide,
        setAdapter: (value: PlayerAdapter | null) => { activeAdapter = value; },
        setError: (value: boolean) => { hasError = value; },
        setNative: (value: NativeMediaOpenResult | null) => { native = value; },
        setNativeFallback: (value: boolean) => { nativeFallback = value; },
        setState: (value: Partial<PlayerState>) => { state = { ...state, ...value }; },
        state: () => state,
    };
}

beforeEach(() => {
    vi.useFakeTimers();
    apiMock.hideNativeSeekThumbnail.mockReset();
    apiMock.moveNativeSeekThumbnail.mockReset();
    apiMock.showNativeSeekThumbnail.mockReset();
    nativeSeekOverlayAvailableMock.mockReset();
    nativeSeekOverlayAvailableMock.mockReturnValue(false);
    vi.stubGlobal('fetch', vi.fn());
    vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:thumbnail');
    vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
        callback(0);
        return 1;
    });
    vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => {});
});

afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    document.body.replaceChildren();
});

async function flushPromises(): Promise<void> {
    await Promise.resolve();
    await Promise.resolve();
}

describe('VideoTransportController sync', () => {
    it('renders timeline, buffered ranges, volume and accessible labels from player state', () => {
        const { controller, dom, refreshFullscreenAvailability } = makeHarness({
            paused: true,
            currentTime: 75,
            duration: 3_665,
            buffered: [{ start: 0, end: 120 }, { start: 200, end: 260 }],
            volume: 0.25,
            muted: false,
        });

        controller.sync({
            ...EMPTY_PLAYER_STATE,
            paused: true,
            currentTime: 75,
            duration: 3_665,
            buffered: [{ start: 0, end: 120 }, { start: 200, end: 260 }],
            volume: 0.25,
            muted: false,
            rate: 1,
            loading: false,
            tracks: [],
        });

        expect(dom.playButton!.dataset.state).toBe('paused');
        expect(dom.playButton!.getAttribute('aria-label')).toBe('Play');
        expect(dom.muteButton!.dataset.state).toBe('unmuted');
        expect(dom.time!.textContent).toBe('1:15');
        expect(dom.duration!.textContent).toBe('1:01:05');
        expect(dom.scrubberPlayed!.style.width).toBe(`${(75 / 3665) * 100}%`);
        expect(dom.scrubber!.getAttribute('aria-valuetext')).toBe('1:15 of 1:01:05');
        expect(dom.scrubberBuffered!.querySelectorAll('.video-scrubber-segment')).toHaveLength(2);
        expect(dom.volumeFill!.style.width).toBe('25%');
        expect(dom.volumeSlider!.getAttribute('aria-valuetext')).toBe('25%');
        expect(dom.centerControls!.getAttribute('aria-hidden')).toBe('false');
        expect(dom.modal!.classList.contains('is-video-paused')).toBe(true);
        expect(refreshFullscreenAvailability).toHaveBeenCalled();
    });

    it('disables skip and scrub controls when no adapter can seek', () => {
        const harness = makeHarness({ duration: 0 });
        harness.setAdapter(null);
        harness.controller.sync(harness.state());

        expect(harness.dom.skipBackButton!.disabled).toBe(true);
        expect(harness.dom.skipForwardButton!.getAttribute('aria-disabled')).toBe('true');
        expect(harness.dom.centerSkipBackButton!.disabled).toBe(true);
        expect(harness.dom.centerSkipForwardButton!.disabled).toBe(true);
        expect(harness.dom.scrubber!.classList.contains('is-disabled')).toBe(true);
        expect(harness.dom.scrubber!.getAttribute('aria-disabled')).toBe('true');
        expect(harness.dom.scrubber!.tabIndex).toBe(-1);
    });

    it('keeps native fallback seek buttons enabled when duration is still unknown', () => {
        const harness = makeHarness({ duration: 0 });
        harness.setNativeFallback(true);
        harness.controller.sync(harness.state());

        expect(harness.dom.skipBackButton!.disabled).toBe(false);
        expect(harness.dom.skipForwardButton!.getAttribute('aria-disabled')).toBe('false');
        expect(harness.dom.scrubber!.classList.contains('is-disabled')).toBe(false);
        expect(harness.dom.scrubber!.tabIndex).toBe(0);
    });

    it('hides the center play overlay while loading and restores focus when it closes', () => {
        const harness = makeHarness({ paused: true, loading: false });
        harness.dom.centerControls!.append(harness.dom.centerPlayButton!);
        harness.dom.modal!.append(harness.dom.playButton!);
        harness.dom.centerPlayButton!.focus();
        harness.controller.sync(harness.state());
        expect(harness.dom.centerControls!.getAttribute('aria-hidden')).toBe('false');

        harness.setState({ loading: true });
        harness.controller.sync(harness.state());

        expect(harness.dom.centerControls!.getAttribute('aria-hidden')).toBe('true');
        expect(document.activeElement).toBe(harness.dom.playButton);
    });
});

describe('VideoTransportController controls', () => {
    it('toggles playback and records whether the user asked to pause or resume', () => {
        const { adapter, controller, dom, markPausedByUser, revealChrome, setState } = makeHarness({ paused: true });
        controller.bind();

        dom.playButton!.click();
        expect(markPausedByUser).toHaveBeenCalledWith(false);
        expect(adapter.playPause).toHaveBeenCalledTimes(1);
        expect(revealChrome).toHaveBeenCalledTimes(1);

        setState({ paused: false });
        dom.centerPlayButton!.click();
        expect(markPausedByUser).toHaveBeenLastCalledWith(true);
        expect(adapter.playPause).toHaveBeenCalledTimes(2);
    });

    it('ignores playback while an error is visible', () => {
        const { adapter, controller, dom, setError } = makeHarness();
        controller.bind();
        setError(true);

        dom.playButton!.click();

        expect(adapter.playPause).not.toHaveBeenCalled();
    });

    it('seeks from buttons and scrubber keyboard shortcuts', () => {
        const { adapter, controller, dom, revealChrome } = makeHarness({ duration: 120 });
        controller.bind();

        dom.skipForwardButton!.click();
        dom.centerSkipBackButton!.click();
        dom.scrubber!.dispatchEvent(keyEvent('ArrowRight'));
        dom.scrubber!.dispatchEvent(keyEvent('Home'));
        dom.scrubber!.dispatchEvent(keyEvent('End'));

        expect(adapter.seekRelative).toHaveBeenNthCalledWith(1, SEEK_STEP_SECONDS);
        expect(adapter.seekRelative).toHaveBeenNthCalledWith(2, -SEEK_STEP_SECONDS);
        expect(adapter.seekRelative).toHaveBeenNthCalledWith(3, SEEK_STEP_SECONDS);
        expect(adapter.seekAbsolute).toHaveBeenNthCalledWith(1, 0);
        expect(adapter.seekAbsolute).toHaveBeenNthCalledWith(2, 120);
        expect(revealChrome).toHaveBeenCalledTimes(2);
    });

    it('previews a scrub while dragging and commits the final pointer position', () => {
        const { adapter, controller, dom } = makeHarness({ duration: 100, currentTime: 10 });
        controller.bind();

        dom.scrubber!.dispatchEvent(pointerEvent('pointerdown', { clientX: 60 }));
        expect(dom.scrubber!.classList.contains('is-dragging')).toBe(true);
        expect(dom.time!.textContent).toBe('0:25');
        expect(dom.scrubber!.getAttribute('aria-valuetext')).toBe('0:25 of 1:40');

        dom.scrubber!.dispatchEvent(pointerEvent('pointermove', { clientX: 110 }));
        expect(dom.time!.textContent).toBe('0:50');

        dom.scrubber!.dispatchEvent(pointerEvent('pointerup', { clientX: 210 }));
        expect(adapter.seekAbsolute).toHaveBeenCalledWith(100);
        expect(dom.scrubber!.classList.contains('is-dragging')).toBe(false);
    });

    it('ignores stray pointer updates and restores the real time after cancel or lost capture', () => {
        const { adapter, controller, dom } = makeHarness({ duration: 100, currentTime: 10 });
        controller.bind();
        controller.sync({ ...EMPTY_PLAYER_STATE, duration: 100, currentTime: 10, volume: 1, rate: 1 });

        dom.scrubber!.dispatchEvent(pointerEvent('pointerdown', { clientX: 60, pointerId: 1 }));
        dom.scrubber!.dispatchEvent(pointerEvent('pointermove', { clientX: 210, pointerId: 2 }));
        expect(dom.time!.textContent).toBe('0:25');

        dom.scrubber!.dispatchEvent(pointerEvent('pointercancel', { clientX: 60, pointerId: 1 }));
        expect(dom.scrubber!.classList.contains('is-dragging')).toBe(false);
        expect(dom.time!.textContent).toBe('0:10');

        dom.scrubber!.dispatchEvent(pointerEvent('pointerdown', { clientX: 110, pointerId: 3 }));
        dom.scrubber!.dispatchEvent(new Event('lostpointercapture'));
        expect(dom.time!.textContent).toBe('0:10');
        expect(adapter.seekAbsolute).not.toHaveBeenCalled();
    });

    it('does not seek from keyboard shortcuts when the adapter or duration is unavailable', () => {
        const harness = makeHarness({ duration: 0 });
        harness.controller.bind();
        harness.dom.scrubber!.dispatchEvent(keyEvent('End'));
        expect(harness.adapter.seekAbsolute).not.toHaveBeenCalled();

        harness.setState({ duration: 100 });
        harness.setAdapter(null);
        harness.dom.scrubber!.dispatchEvent(keyEvent('ArrowRight'));
        expect(harness.adapter.seekRelative).not.toHaveBeenCalled();
    });

    it('ignores non-primary scrub starts and pointer-up events from a different pointer', () => {
        const { adapter, controller, dom } = makeHarness({ duration: 100, currentTime: 10 });
        controller.bind();

        dom.scrubber!.dispatchEvent(pointerEvent('pointerdown', { clientX: 60, isPrimary: false }));
        expect(dom.scrubber!.classList.contains('is-dragging')).toBe(false);

        dom.scrubber!.dispatchEvent(pointerEvent('pointerdown', { clientX: 60, pointerId: 1 }));
        dom.scrubber!.dispatchEvent(pointerEvent('pointerup', { clientX: 210, pointerId: 2 }));

        expect(adapter.seekAbsolute).not.toHaveBeenCalled();
        expect(dom.scrubber!.classList.contains('is-dragging')).toBe(true);
    });

    it('updates volume through mute, keyboard and pointer controls', () => {
        const { adapter, controller, dom } = makeHarness({ volume: 0.4, muted: false });
        controller.bind();

        dom.muteButton!.click();
        dom.volumeSlider!.dispatchEvent(keyEvent('ArrowDown'));
        dom.volumeSlider!.dispatchEvent(keyEvent('End'));
        dom.volumeSlider!.dispatchEvent(pointerEvent('pointerdown', { clientX: 45 }));

        expect(adapter.setMuted).toHaveBeenCalledWith(true);
        expect(adapter.setVolume).toHaveBeenNthCalledWith(1, 0.4 - VOLUME_STEP);
        expect(adapter.setVolume).toHaveBeenNthCalledWith(2, 1);
        expect(adapter.setVolume).toHaveBeenNthCalledWith(3, 0.25);
        expect(dom.volumeFill!.style.width).toBe('25%');
        expect(dom.volumeSlider!.getAttribute('aria-valuetext')).toBe('25%');
    });

    it('does not change volume from the keyboard when no adapter is active', () => {
        const harness = makeHarness({ volume: 0.4 });
        harness.controller.bind();
        harness.setAdapter(null);

        harness.dom.volumeSlider!.dispatchEvent(keyEvent('ArrowUp'));

        expect(harness.adapter.setVolume).not.toHaveBeenCalled();
    });

    it('does not start pointer volume changes when no adapter is active', () => {
        const harness = makeHarness({ volume: 0.4 });
        harness.controller.bind();
        harness.setAdapter(null);

        harness.dom.muteButton!.click();
        harness.dom.volumeSlider!.dispatchEvent(pointerEvent('pointerdown', { clientX: 45 }));
        harness.dom.volumeSlider!.dispatchEvent(pointerEvent('pointermove', { clientX: 90 }));

        expect(harness.adapter.setMuted).not.toHaveBeenCalled();
        expect(harness.adapter.setVolume).not.toHaveBeenCalled();
    });

    it('coalesces rapid pointer volume updates into the latest animation-frame value', () => {
        vi.mocked(window.requestAnimationFrame).mockReturnValue(7);
        const { adapter, controller, dom } = makeHarness({ volume: 0.4 });
        controller.bind();

        dom.volumeSlider!.dispatchEvent(pointerEvent('pointerdown', { clientX: 30 }));
        dom.volumeSlider!.dispatchEvent(pointerEvent('pointermove', { clientX: 90 }));
        expect(adapter.setVolume).not.toHaveBeenCalled();

        expect(window.requestAnimationFrame).toHaveBeenCalledTimes(1);
        const [[frame]] = vi.mocked(window.requestAnimationFrame).mock.calls;
        frame(0);
        expect(adapter.setVolume).toHaveBeenCalledExactlyOnceWith(0.7);
    });

    it('shows and clears end-time mode without native tooltips', () => {
        const { controller, dom, geometryChanged, revealChrome } = makeHarness({
            paused: true,
            currentTime: 60,
            duration: 120,
            rate: 1,
        });
        controller.bind();
        controller.sync({ ...EMPTY_PLAYER_STATE, paused: true, currentTime: 60, duration: 120, volume: 1, rate: 1 });

        dom.timeDisplay!.click();
        expect(dom.timeDisplay!.getAttribute('aria-pressed')).toBe('true');
        expect(dom.timeDisplay!.getAttribute('aria-label')).toContain('if you resume now');
        expect(dom.endTime!.classList.contains('is-visible')).toBe(true);
        expect(dom.endTime!.textContent).toContain('Ends at');

        dom.timeDisplay!.click();
        expect(dom.timeDisplay!.getAttribute('aria-pressed')).toBe('false');
        expect(dom.endTime!.classList.contains('is-visible')).toBe(false);
        expect(geometryChanged).toHaveBeenCalledTimes(2);
        expect(revealChrome).toHaveBeenCalledTimes(2);
    });

    it('cleans transient chrome and thumbnail state when a session resets', () => {
        const { controller, dom, scheduleChromeHide } = makeHarness({ duration: 100 });
        controller.bind();
        controller.seekBy(SEEK_STEP_SECONDS);
        dom.scrubber!.dispatchEvent(pointerEvent('pointerenter', { clientX: 110 }));
        expect(controller.isScrubberTooltipActive()).toBe(true);
        expect(dom.skipFeedback!.classList.contains('is-visible')).toBe(true);

        controller.resetSession();

        expect(controller.isScrubberTooltipActive()).toBe(false);
        expect(dom.skipFeedback!.classList.contains('is-visible')).toBe(false);
        expect(dom.scrubberTooltip!.classList.contains('is-thumbnail-pending')).toBe(false);

        dom.scrubber!.dispatchEvent(pointerEvent('pointerleave', { clientX: 110 }));
        expect(scheduleChromeHide).toHaveBeenCalled();
    });

    it('fetches the hovered thumbnail bucket and keeps nearby frames warm after dwell', async () => {
        const { controller, dom } = makeHarness({ duration: 120 });
        const fetchMock = vi.mocked(fetch);
        fetchMock.mockResolvedValue(new Response(new Blob(['thumb'], { type: 'image/jpeg' })));
        controller.bind();
        controller.beginSession('/thumbs/video');

        dom.scrubber!.dispatchEvent(pointerEvent('pointerenter', { clientX: 110 }));
        expect(dom.scrubberTooltipTime!.textContent).toBe('1:00');
        expect(dom.scrubberTooltip!.classList.contains('is-thumbnail-pending')).toBe(true);

        await vi.advanceTimersByTimeAsync(140);
        await flushPromises();

        expect(fetchMock).toHaveBeenCalledWith('/thumbs/video?t=60', { cache: 'no-store' });
        expect(dom.scrubberTooltipImage!.getAttribute('src')).toBe('blob:thumbnail');
        expect(dom.scrubberTooltip!.classList.contains('has-thumbnail')).toBe(true);

        await vi.advanceTimersByTimeAsync(420);
        await flushPromises();

        expect(fetchMock).toHaveBeenCalledWith('/thumbs/video?t=50', { cache: 'no-store' });
        expect(fetchMock).toHaveBeenCalledWith('/thumbs/video?t=70', { cache: 'no-store' });
    });

    it('shows an unknown-time native fallback thumbnail state before duration is known', () => {
        const { controller, dom, setNativeFallback } = makeHarness({ duration: 0 });
        setNativeFallback(true);
        controller.bind();
        controller.beginSession('/thumbs/video');

        dom.scrubber!.dispatchEvent(pointerEvent('pointerenter', { clientX: 110 }));

        expect(dom.scrubberTooltipTime!.textContent).toBe('--:--');
        expect(dom.scrubberTooltip!.classList.contains('is-thumbnail-pending')).toBe(true);
        expect(dom.scrubberTooltip!.style.left).toBe('100px');
        expect(fetch).not.toHaveBeenCalled();
    });

    it('uses a nearby cached thumbnail while requesting the exact bucket', async () => {
        const { controller, dom } = makeHarness({ duration: 120 });
        const fetchMock = vi.mocked(fetch);
        fetchMock.mockResolvedValue(new Response(new Blob(['thumb'], { type: 'image/jpeg' })));
        controller.bind();
        controller.beginSession('/thumbs/video');

        dom.scrubber!.dispatchEvent(pointerEvent('pointerenter', { clientX: 110 }));
        await vi.advanceTimersByTimeAsync(140);
        await flushPromises();
        expect(dom.scrubberTooltipImage!.getAttribute('src')).toBe('blob:thumbnail');

        dom.scrubber!.dispatchEvent(pointerEvent('pointermove', { clientX: 130 }));
        expect(dom.scrubberTooltipImage!.getAttribute('src')).toBe('blob:thumbnail');
        expect(dom.scrubberTooltip!.classList.contains('has-thumbnail')).toBe(true);

        await vi.advanceTimersByTimeAsync(140);
        await flushPromises();

        expect(fetchMock).toHaveBeenCalledWith('/thumbs/video?t=70', { cache: 'no-store' });
    });

    it('does not install a thumbnail response that arrives after the session changes', async () => {
        const { controller, dom } = makeHarness({ duration: 120 });
        let resolveFetch: (response: Response) => void = () => {};
        vi.mocked(fetch).mockReturnValue(new Promise<Response>((resolve) => {
            resolveFetch = resolve;
        }));
        controller.bind();
        controller.beginSession('/thumbs/video');

        dom.scrubber!.dispatchEvent(pointerEvent('pointerenter', { clientX: 110 }));
        await vi.advanceTimersByTimeAsync(140);
        controller.resetSession();
        resolveFetch(new Response(new Blob(['late'], { type: 'image/jpeg' })));
        await flushPromises();

        expect(URL.createObjectURL).not.toHaveBeenCalled();
        expect(dom.scrubberTooltipImage!.hasAttribute('src')).toBe(false);
    });

    it('leaves the tooltip pending when a successful thumbnail response has no bytes', async () => {
        const { controller, dom } = makeHarness({ duration: 120 });
        vi.mocked(fetch).mockResolvedValue(new Response(new Blob([])));
        controller.bind();
        controller.beginSession('/thumbs/video');

        dom.scrubber!.dispatchEvent(pointerEvent('pointerenter', { clientX: 110 }));
        await vi.advanceTimersByTimeAsync(140);
        await flushPromises();

        expect(URL.createObjectURL).not.toHaveBeenCalled();
        expect(dom.scrubberTooltip!.classList.contains('is-thumbnail-pending')).toBe(true);
    });

    it('widens thumbnail buckets for long and very long videos', async () => {
        const { controller, dom, setState } = makeHarness({ duration: 45 * 60 });
        vi.mocked(fetch).mockResolvedValue(new Response(new Blob(['thumb'], { type: 'image/jpeg' })));
        controller.bind();
        controller.beginSession('/thumbs/video');

        dom.scrubber!.dispatchEvent(pointerEvent('pointerenter', { clientX: 110 }));
        await vi.advanceTimersByTimeAsync(140);
        await flushPromises();
        expect(fetch).toHaveBeenCalledWith('/thumbs/video?t=1360', { cache: 'no-store' });

        controller.resetSession();
        controller.beginSession('/thumbs/video');
        setState({ duration: 3 * 60 * 60 });
        dom.scrubber!.dispatchEvent(pointerEvent('pointermove', { clientX: 110 }));
        await vi.advanceTimersByTimeAsync(140);
        await flushPromises();

        expect(fetch).toHaveBeenCalledWith('/thumbs/video?t=5400', { cache: 'no-store' });
    });

    it('retries a pending thumbnail response and marks hard failures visibly', async () => {
        const { controller, dom } = makeHarness({ duration: 120 });
        const fetchMock = vi.mocked(fetch);
        fetchMock
            .mockResolvedValueOnce(new Response(null, { status: 202 }))
            .mockResolvedValueOnce(new Response(null, { status: 500 }));
        controller.bind();
        controller.beginSession('/thumbs/video');

        dom.scrubber!.dispatchEvent(pointerEvent('pointerenter', { clientX: 110 }));
        await vi.advanceTimersByTimeAsync(140);
        await flushPromises();
        expect(fetchMock).toHaveBeenCalledTimes(1);

        await vi.advanceTimersByTimeAsync(650);
        await flushPromises();

        expect(fetchMock).toHaveBeenCalledTimes(2);
        expect(dom.scrubberTooltip!.classList.contains('is-thumbnail-failed')).toBe(true);
    });

    it('uploads a native seek thumbnail once and moves it while the bucket stays cached', async () => {
        const { controller, dom, setNative, setNativeFallback } = makeHarness({ duration: 120 });
        nativeSeekOverlayAvailableMock.mockReturnValue(true);
        setNativeFallback(true);
        setNative({ token: 'fixture' } as NativeMediaOpenResult);
        Object.defineProperties(dom.scrubberTooltipImage!, {
            naturalWidth: { configurable: true, value: 160 },
            naturalHeight: { configurable: true, value: 90 },
        });
        vi.mocked(fetch).mockResolvedValue(new Response(new Blob(['thumb'], { type: 'image/jpeg' })));
        controller.bind();
        controller.beginSession('/thumbs/video');

        dom.scrubber!.dispatchEvent(pointerEvent('pointerenter', { clientX: 110 }));
        await vi.advanceTimersByTimeAsync(140);
        await flushPromises();

        expect(apiMock.showNativeSeekThumbnail).toHaveBeenCalledWith(
            'fixture',
            expect.any(String),
            expect.objectContaining({ width: 144, height: 81 }),
        );

        dom.scrubber!.dispatchEvent(pointerEvent('pointermove', { clientX: 112 }));
        await vi.advanceTimersByTimeAsync(16);
        expect(apiMock.moveNativeSeekThumbnail).toHaveBeenCalledWith(
            'fixture',
            expect.objectContaining({ width: 144, height: 81 }),
        );

        dom.scrubber!.dispatchEvent(pointerEvent('pointerleave', { clientX: 112 }));
        expect(apiMock.hideNativeSeekThumbnail).toHaveBeenCalledWith('fixture');
    });

    it('converts an already-cached web thumbnail when native fallback takes over', async () => {
        const { controller, dom, setNative, setNativeFallback } = makeHarness({ duration: 120 });
        nativeSeekOverlayAvailableMock.mockReturnValue(true);
        Object.defineProperties(dom.scrubberTooltipImage!, {
            naturalWidth: { configurable: true, value: 160 },
            naturalHeight: { configurable: true, value: 90 },
        });
        vi.mocked(fetch)
            .mockResolvedValueOnce(new Response(new Blob(['web-thumb'], { type: 'image/jpeg' })))
            .mockResolvedValueOnce(new Response(new Blob(['native-thumb'], { type: 'image/jpeg' })));
        controller.bind();
        controller.beginSession('/thumbs/video');

        dom.scrubber!.dispatchEvent(pointerEvent('pointerenter', { clientX: 110 }));
        await vi.advanceTimersByTimeAsync(140);
        await flushPromises();
        expect(apiMock.showNativeSeekThumbnail).not.toHaveBeenCalled();

        setNativeFallback(true);
        setNative({ token: 'fixture' } as NativeMediaOpenResult);
        dom.scrubber!.dispatchEvent(pointerEvent('pointermove', { clientX: 110 }));
        await flushPromises();
        await flushPromises();

        expect(fetch).toHaveBeenCalledWith('blob:thumbnail');
        expect(apiMock.showNativeSeekThumbnail).toHaveBeenCalledWith(
            'fixture',
            expect.any(String),
            expect.objectContaining({ width: 144, height: 81 }),
        );
    });

    it('cancels a pending thumbnail request when the session resets', async () => {
        const { controller, dom } = makeHarness({ duration: 120 });
        controller.bind();
        controller.beginSession('/thumbs/video');

        dom.scrubber!.dispatchEvent(pointerEvent('pointerenter', { clientX: 110 }));
        controller.resetSession();
        await vi.advanceTimersByTimeAsync(140);

        expect(fetch).not.toHaveBeenCalled();
    });

    it('marks the thumbnail tooltip as failed when fetching rejects', async () => {
        const { controller, dom } = makeHarness({ duration: 120 });
        vi.mocked(fetch).mockRejectedValue(new Error('offline'));
        controller.bind();
        controller.beginSession('/thumbs/video');

        dom.scrubber!.dispatchEvent(pointerEvent('pointerenter', { clientX: 110 }));
        await vi.advanceTimersByTimeAsync(140);
        await flushPromises();

        expect(dom.scrubberTooltip!.classList.contains('is-thumbnail-failed')).toBe(true);
    });

    it('does not pile up duplicate thumbnail requests for the same pending bucket', async () => {
        const { controller, dom } = makeHarness({ duration: 120 });
        let resolveFetch: (response: Response) => void = () => {};
        vi.mocked(fetch).mockReturnValue(new Promise<Response>((resolve) => {
            resolveFetch = resolve;
        }));
        controller.bind();
        controller.beginSession('/thumbs/video');

        dom.scrubber!.dispatchEvent(pointerEvent('pointerenter', { clientX: 110 }));
        await vi.advanceTimersByTimeAsync(140);
        dom.scrubber!.dispatchEvent(pointerEvent('pointermove', { clientX: 110 }));
        await vi.advanceTimersByTimeAsync(140);
        expect(fetch).toHaveBeenCalledTimes(1);

        resolveFetch(new Response(new Blob(['thumb'], { type: 'image/jpeg' })));
        await flushPromises();
    });

    it('suppresses thumbnail refetch during the failure TTL and retries after it expires', async () => {
        const { controller, dom } = makeHarness({ duration: 120 });
        vi.mocked(fetch)
            .mockRejectedValueOnce(new Error('offline'))
            .mockResolvedValue(new Response(new Blob(['thumb'], { type: 'image/jpeg' })));
        controller.bind();
        controller.beginSession('/thumbs/video');

        dom.scrubber!.dispatchEvent(pointerEvent('pointerenter', { clientX: 110 }));
        await vi.advanceTimersByTimeAsync(140);
        await flushPromises();
        expect(fetch).toHaveBeenCalledTimes(1);

        dom.scrubber!.dispatchEvent(pointerEvent('pointermove', { clientX: 110 }));
        await vi.advanceTimersByTimeAsync(140);
        expect(fetch).toHaveBeenCalledTimes(1);

        await vi.advanceTimersByTimeAsync(15_000);
        dom.scrubber!.dispatchEvent(pointerEvent('pointermove', { clientX: 110 }));
        await vi.advanceTimersByTimeAsync(140);
        await flushPromises();

        expect(fetch).toHaveBeenCalledTimes(2);
        expect(dom.scrubberTooltip!.classList.contains('has-thumbnail')).toBe(true);
    });
});
