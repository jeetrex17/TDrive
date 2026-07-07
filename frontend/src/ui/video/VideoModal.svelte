<div id="video-shell" class="video-shell" role="dialog" aria-modal="true" aria-labelledby="video-filename" tabindex="-1">
    <div class="video-topbar">
        <div class="video-title-group">
            <div id="video-filename" class="video-filename"></div>
            <div id="video-meta" class="video-meta"></div>
        </div>
        <button id="video-close" class="video-icon-btn video-close-btn" type="button" aria-label="Close video" title="Close">×</button>
    </div>

    <div id="video-stage" class="video-stage">
        <div id="video-native-viewport" class="video-native-viewport" aria-hidden="true"></div>
        <video id="video-player" class="video-player" playsinline preload="metadata"></video>

        <div id="video-center-controls" class="video-center-controls" aria-hidden="true">
            <button id="video-center-skip-back" class="video-center-skip video-center-skip-back" type="button" aria-label="Back 10 seconds" title="Back 10 seconds">
                <svg viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="M11 5H6v5" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><path d="M6.6 8.3A7 7 0 1112 19" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><text x="12" y="15.2" text-anchor="middle" fill="currentColor" font-size="6.2" font-weight="800">10</text></svg>
            </button>
            <button id="video-center-play" class="video-center-play" type="button" aria-label="Play video" title="Play">
                <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M8 5.8c0-.9.98-1.45 1.74-.98l9.08 5.67a1.15 1.15 0 010 1.96l-9.08 5.67A1.15 1.15 0 018 17.14V5.8z"/></svg>
            </button>
            <button id="video-center-skip-forward" class="video-center-skip video-center-skip-forward" type="button" aria-label="Forward 10 seconds" title="Forward 10 seconds">
                <svg viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="M13 5h5v5" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><path d="M17.4 8.3A7 7 0 1012 19" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><text x="12" y="15.2" text-anchor="middle" fill="currentColor" font-size="6.2" font-weight="800">10</text></svg>
            </button>
        </div>

        <div id="video-skip-feedback" class="video-skip-feedback" aria-hidden="true"><span></span></div>

        <div id="video-loading" class="video-loading" aria-hidden="true" style="display: none;">
            <div class="video-spinner"></div>
            <div id="video-loading-status" class="video-loading-status" role="status" aria-live="polite">Opening video</div>
        </div>

        <div id="video-error" class="video-error" role="alert" style="display: none;"></div>
    </div>

    <div class="video-controls" aria-label="Video controls">
        <div class="video-timeline-row">
            <span id="video-time" class="video-time">0:00</span>
            <div id="video-scrubber" class="video-scrubber" role="slider" tabindex="0" aria-label="Seek" aria-valuemin="0" aria-valuemax="0" aria-valuenow="0" aria-valuetext="0:00">
                <div class="video-scrubber-hit">
                    <span class="video-scrubber-track"></span>
                    <span id="video-scrubber-buffered" class="video-scrubber-buffered"></span>
                    <span id="video-scrubber-played" class="video-scrubber-played"></span>
                    <span id="video-scrubber-thumb" class="video-scrubber-thumb"></span>
                    <span id="video-scrubber-tooltip" class="video-scrubber-tooltip">
                        <img id="video-scrubber-tooltip-image" class="video-scrubber-tooltip-image" alt="" aria-hidden="true">
                        <span id="video-scrubber-tooltip-time" class="video-scrubber-tooltip-time">0:00</span>
                    </span>
                </div>
            </div>
            <span id="video-duration" class="video-time">--:--</span>
        </div>

        <div class="video-command-row">
            <div class="video-command-cluster video-primary-cluster">
                <button id="video-skip-back" class="video-icon-btn video-skip-btn" type="button" aria-label="Back 10 seconds" title="Back 10 seconds">
                    <svg viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="M11 5H6v5" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><path d="M6.6 8.3A7 7 0 1112 19" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><text x="12" y="15.2" text-anchor="middle" fill="currentColor" font-size="6.2" font-weight="800">10</text></svg>
                </button>
                <button id="video-play" class="video-icon-btn video-play-btn" type="button" data-state="paused" aria-label="Play" title="Play">
                    <svg class="video-play-symbol video-symbol-play" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M8 5.8c0-.9.98-1.45 1.74-.98l9.08 5.67a1.15 1.15 0 010 1.96l-9.08 5.67A1.15 1.15 0 018 17.14V5.8z"/></svg>
                    <svg class="video-play-symbol video-symbol-pause" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M7 5h3.4v14H7V5zm6.6 0H17v14h-3.4V5z"/></svg>
                </button>
                <button id="video-skip-forward" class="video-icon-btn video-skip-btn" type="button" aria-label="Forward 10 seconds" title="Forward 10 seconds">
                    <svg viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="M13 5h5v5" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><path d="M17.4 8.3A7 7 0 1012 19" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><text x="12" y="15.2" text-anchor="middle" fill="currentColor" font-size="6.2" font-weight="800">10</text></svg>
                </button>
            </div>

            <div class="video-command-cluster video-secondary-cluster">
                <div class="video-volume-group">
                    <button id="video-mute" class="video-icon-btn video-mute-btn" type="button" data-state="unmuted" aria-label="Mute" title="Mute">
                        <svg class="video-mute-symbol video-symbol-volume" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path stroke-linecap="round" stroke-linejoin="round" d="M4 10v4h4l5 4V6l-5 4H4z"/><path stroke-linecap="round" stroke-linejoin="round" d="M16 9.5a4 4 0 010 5"/></svg>
                        <svg class="video-mute-symbol video-symbol-muted" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path stroke-linecap="round" stroke-linejoin="round" d="M4 10v4h4l5 4V6l-5 4H4z"/><path stroke-linecap="round" stroke-linejoin="round" d="M18 9l-5 6m0-6l5 6"/></svg>
                    </button>
                    <div id="video-volume-slider" class="video-volume-slider" role="slider" tabindex="0" aria-label="Volume" aria-valuemin="0" aria-valuemax="100" aria-valuenow="100" aria-valuetext="100%">
                        <span class="video-volume-track"></span>
                        <span id="video-volume-fill" class="video-volume-fill"></span>
                        <span id="video-volume-thumb" class="video-volume-thumb"></span>
                    </div>
                </div>

                <div class="video-speed-wrap">
                    <button id="video-speed-button" class="video-speed-button" type="button" aria-haspopup="menu" aria-expanded="false">1x</button>
                    <div id="video-speed-menu" class="video-speed-menu" role="menu" aria-label="Playback speed"></div>
                </div>

                <button id="video-fullscreen" class="video-icon-btn video-fullscreen-btn" type="button" data-state="windowed" aria-label="Enter fullscreen" title="Enter fullscreen">
                    <svg class="video-fullscreen-symbol video-symbol-fullscreen-enter" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M8 3H3v5M16 3h5v5M8 21H3v-5M21 16v5h-5" stroke-linecap="round" stroke-linejoin="round"/></svg>
                    <svg class="video-fullscreen-symbol video-symbol-fullscreen-exit" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M9 3v6H3M15 3v6h6M9 21v-6H3M21 15h-6v6" stroke-linecap="round" stroke-linejoin="round"/></svg>
                </button>
            </div>
        </div>
    </div>
</div>
