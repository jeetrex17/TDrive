export interface TrackPickerDOM {
    wrap: HTMLElement | null;
    button: HTMLButtonElement | null;
    label: HTMLElement | null;
    menu: HTMLElement | null;
}

export interface VideoDOM {
    modal: HTMLElement | null;
    stage: HTMLElement | null;
    topbar: HTMLElement | null;
    controls: HTMLElement | null;
    filename: HTMLElement | null;
    meta: HTMLElement | null;
    closeButton: HTMLButtonElement | null;
    nativeViewport: HTMLElement | null;
    standalone: HTMLElement | null;
    video: HTMLVideoElement | null;
    loading: HTMLElement | null;
    loadingStatus: HTMLElement | null;
    error: HTMLElement | null;
    centerControls: HTMLElement | null;
    centerPlayButton: HTMLButtonElement | null;
    centerSkipBackButton: HTMLButtonElement | null;
    centerSkipForwardButton: HTMLButtonElement | null;
    skipFeedback: HTMLElement | null;
    playButton: HTMLButtonElement | null;
    skipBackButton: HTMLButtonElement | null;
    skipForwardButton: HTMLButtonElement | null;
    muteButton: HTMLButtonElement | null;
    fullscreenButton: HTMLButtonElement | null;
    scrubber: HTMLElement | null;
    scrubberPlayed: HTMLElement | null;
    scrubberBuffered: HTMLElement | null;
    scrubberThumb: HTMLElement | null;
    scrubberTooltip: HTMLElement | null;
    scrubberTooltipImage: HTMLImageElement | null;
    scrubberTooltipTime: HTMLElement | null;
    volumeSlider: HTMLElement | null;
    volumeFill: HTMLElement | null;
    volumeThumb: HTMLElement | null;
    time: HTMLElement | null;
    duration: HTMLElement | null;
    timeDisplay: HTMLButtonElement | null;
    endTime: HTMLElement | null;
    speedButton: HTMLButtonElement | null;
    speedMenu: HTMLElement | null;
    audioPicker: TrackPickerDOM;
    subtitlePicker: TrackPickerDOM;
}

export interface VideoDOMHandlers {
    close(): void;
    toggleFullscreen(): void;
    pointerMove(): void;
    stageClick(event: MouseEvent): void;
    keydown(event: KeyboardEvent): void;
    resize(): void;
}

export function byID<T extends HTMLElement>(id: string): T | null {
    return document.getElementById(id) as T | null;
}

export function collectVideoDOM(): VideoDOM {
    return {
        modal: byID('video-modal'),
        stage: byID('video-stage'),
        topbar: document.querySelector<HTMLElement>('#video-modal .video-topbar'),
        controls: document.querySelector<HTMLElement>('#video-modal .video-controls'),
        filename: byID('video-filename'),
        meta: byID('video-meta'),
        closeButton: byID('video-close'),
        nativeViewport: byID('video-native-viewport'),
        standalone: byID('video-standalone'),
        video: byID('video-player'),
        loading: byID('video-loading'),
        loadingStatus: byID('video-loading-status'),
        error: byID('video-error'),
        centerControls: byID('video-center-controls'),
        centerPlayButton: byID('video-center-play'),
        centerSkipBackButton: byID('video-center-skip-back'),
        centerSkipForwardButton: byID('video-center-skip-forward'),
        skipFeedback: byID('video-skip-feedback'),
        playButton: byID('video-play'),
        skipBackButton: byID('video-skip-back'),
        skipForwardButton: byID('video-skip-forward'),
        muteButton: byID('video-mute'),
        fullscreenButton: byID('video-fullscreen'),
        scrubber: byID('video-scrubber'),
        scrubberPlayed: byID('video-scrubber-played'),
        scrubberBuffered: byID('video-scrubber-buffered'),
        scrubberThumb: byID('video-scrubber-thumb'),
        scrubberTooltip: byID('video-scrubber-tooltip'),
        scrubberTooltipImage: byID('video-scrubber-tooltip-image'),
        scrubberTooltipTime: byID('video-scrubber-tooltip-time'),
        volumeSlider: byID('video-volume-slider'),
        volumeFill: byID('video-volume-fill'),
        volumeThumb: byID('video-volume-thumb'),
        time: byID('video-time'),
        duration: byID('video-duration'),
        timeDisplay: byID('video-time-display'),
        endTime: byID('video-end-time'),
        speedButton: byID('video-speed-button'),
        speedMenu: byID('video-speed-menu'),
        audioPicker: {
            wrap: byID('video-audio-wrap'),
            button: byID('video-audio-button'),
            label: byID('video-audio-label'),
            menu: byID('video-audio-menu'),
        },
        subtitlePicker: {
            wrap: byID('video-subtitle-wrap'),
            button: byID('video-subtitle-button'),
            label: byID('video-subtitle-label'),
            menu: byID('video-subtitle-menu'),
        },
    };
}

export function bindVideoDOM(dom: VideoDOM, handlers: VideoDOMHandlers): () => void {
    const cleanups: Array<() => void> = [];
    const listen = (target: EventTarget | null, type: string, listener: EventListener): void => {
        if (!target) return;
        target.addEventListener(type, listener);
        cleanups.push(() => target.removeEventListener(type, listener));
    };
    listen(dom.closeButton, 'click', handlers.close as EventListener);
    listen(dom.fullscreenButton, 'click', handlers.toggleFullscreen as EventListener);
    listen(dom.modal, 'pointermove', handlers.pointerMove as EventListener);
    listen(dom.stage, 'click', handlers.stageClick as EventListener);
    listen(document, 'keydown', handlers.keydown as EventListener);
    listen(window, 'resize', handlers.resize as EventListener);
    return () => {
        for (const cleanup of cleanups.splice(0)) cleanup();
    };
}
