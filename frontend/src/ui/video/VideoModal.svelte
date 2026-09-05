<script lang="ts">
    import VideoSettingsPanel from "./VideoSettingsPanel.svelte";
    import { DEFAULT_PLAYBACK_PREFERENCES, type PlaybackPreferences } from "../../modules/video/playback-preferences";
    let { initialPreferences = DEFAULT_PLAYBACK_PREFERENCES, onPreferencesChange = () => undefined }: { initialPreferences?: PlaybackPreferences; onPreferencesChange?: (value: PlaybackPreferences) => void } = $props();
    import AudioLinesIcon from '@lucide/svelte/icons/audio-lines';
    import CaptionsIcon from '@lucide/svelte/icons/captions';
    import MaximizeIcon from '@lucide/svelte/icons/maximize';
    import MinimizeIcon from '@lucide/svelte/icons/minimize';
    import PauseIcon from '@lucide/svelte/icons/pause';
    import PlayIcon from '@lucide/svelte/icons/play';
    import RatioIcon from '@lucide/svelte/icons/ratio';
    import Volume2Icon from '@lucide/svelte/icons/volume-2';
    import VolumeXIcon from '@lucide/svelte/icons/volume-x';
    import XIcon from '@lucide/svelte/icons/x';
</script>

<div id="video-shell" class="video-shell" role="dialog" aria-modal="true" aria-labelledby="video-filename" tabindex="-1">
    <div class="video-topbar">
        <div class="video-title-group">
            <div id="video-filename" class="video-filename"></div>
            <div id="video-meta" class="video-meta"></div>
        </div>
        <button id="video-close" class="video-icon-btn video-close-btn" type="button" aria-label="Close video" title="Close">
            <XIcon size={22} aria-hidden="true" />
        </button>
    </div>

    <div id="video-stage" class="video-stage">
        <div id="video-native-viewport" class="video-native-viewport" aria-hidden="true"></div>
        <video id="video-player" class="video-player" playsinline preload="metadata"></video>

        <div id="video-center-controls" class="video-center-controls" aria-hidden="true">
            <button id="video-center-skip-back" class="video-center-skip video-center-skip-back" type="button" aria-label="Back 10 seconds" title="Back 10 seconds">
                <svg viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="M11 5H6v5" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><path d="M6.6 8.3A7 7 0 1112 19" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><text x="12" y="15.2" text-anchor="middle" fill="currentColor" font-size="6.2" font-weight="800">10</text></svg>
            </button>
            <button id="video-center-play" class="video-center-play" type="button" aria-label="Play video" title="Play">
                <PlayIcon fill="currentColor" aria-hidden="true" />
            </button>
            <button id="video-center-skip-forward" class="video-center-skip video-center-skip-forward" type="button" aria-label="Forward 10 seconds" title="Forward 10 seconds">
                <svg viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="M13 5h5v5" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><path d="M17.4 8.3A7 7 0 1012 19" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><text x="12" y="15.2" text-anchor="middle" fill="currentColor" font-size="6.2" font-weight="800">10</text></svg>
            </button>
        </div>

        <div id="video-skip-feedback" class="video-skip-feedback" aria-hidden="true"><span></span></div>

        <div id="video-standalone" class="video-standalone" role="status" aria-live="polite" hidden>
            <strong>Playing in a separate window</strong>
            <span>Use the player window for playback controls. Closing this preview stops the video.</span>
        </div>

        <div id="video-loading" class="video-loading" aria-hidden="true" style="display: none;">
            <div class="video-spinner"></div>
            <div id="video-loading-status" class="video-loading-status" role="status" aria-live="polite" aria-atomic="true">Opening video</div>
        </div>

        <div id="video-error" class="video-error" role="alert" style="display: none;"></div>
    </div>

    <aside id="video-settings-panel" class="video-settings-panel" aria-label="Playback settings" hidden>
        <div class="video-settings-heading"><strong>Playback settings</strong><button id="video-settings-close" type="button" aria-label="Close playback settings"><XIcon size={18} aria-hidden="true" /></button></div>
        <nav class="video-settings-tabs" aria-label="Playback settings sections">
            <button type="button" data-settings-section="picture">Picture</button>
            <button type="button" data-settings-section="audio">Audio</button>
            <button type="button" data-settings-section="subtitle">Subtitles</button>
            <button type="button" data-settings-section="speed">Speed</button>
        </nav>
        <div class="video-settings-body">
            <div id="video-audio-menu" class="video-menu video-track-menu" role="group" aria-label="Audio track"></div>
            <div id="video-subtitle-menu" class="video-menu video-track-menu" role="group" aria-label="Subtitles"></div>
            <div id="video-speed-menu" class="video-menu video-speed-menu" role="menu" aria-label="Playback speed"></div>
            <VideoSettingsPanel {initialPreferences} {onPreferencesChange} />
        </div>
    </aside>

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
                    <PlayIcon class="video-play-symbol video-symbol-play" fill="currentColor" aria-hidden="true" />
                    <PauseIcon class="video-play-symbol video-symbol-pause" fill="currentColor" aria-hidden="true" />
                </button>
                <button id="video-skip-forward" class="video-icon-btn video-skip-btn" type="button" aria-label="Forward 10 seconds" title="Forward 10 seconds">
                    <svg viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="M13 5h5v5" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><path d="M17.4 8.3A7 7 0 1012 19" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><text x="12" y="15.2" text-anchor="middle" fill="currentColor" font-size="6.2" font-weight="800">10</text></svg>
                </button>
            </div>

            <div class="video-command-cluster video-secondary-cluster">
                <div class="video-volume-group">
                    <button id="video-mute" class="video-icon-btn video-mute-btn" type="button" data-state="unmuted" aria-label="Mute" title="Mute">
                        <Volume2Icon class="video-mute-symbol video-symbol-volume" aria-hidden="true" />
                        <VolumeXIcon class="video-mute-symbol video-symbol-muted" aria-hidden="true" />
                    </button>
                    <div id="video-volume-slider" class="video-volume-slider" role="slider" tabindex="0" aria-label="Volume" aria-valuemin="0" aria-valuemax="100" aria-valuenow="100" aria-valuetext="100%">
                        <span class="video-volume-track"></span>
                        <span id="video-volume-fill" class="video-volume-fill"></span>
                        <span id="video-volume-thumb" class="video-volume-thumb"></span>
                    </div>
                </div>

                <div id="video-audio-wrap" class="video-menu-wrap video-track-wrap" hidden>
                    <button id="video-audio-button" class="video-pill-button video-track-button" type="button" aria-controls="video-settings-panel" aria-expanded="false" aria-label="Audio track" title="Audio track">
                        <AudioLinesIcon size={15} aria-hidden="true" />
                        <span id="video-audio-label" class="video-track-value"></span>
                    </button>
                </div>

                <div id="video-subtitle-wrap" class="video-menu-wrap video-track-wrap" hidden>
                    <button id="video-subtitle-button" class="video-pill-button video-track-button" type="button" data-state="off" aria-controls="video-settings-panel" aria-expanded="false" aria-label="Subtitles" title="Subtitles">
                        <CaptionsIcon size={15} aria-hidden="true" />
                        <span id="video-subtitle-label" class="video-track-value"></span>
                    </button>
                </div>

                <div class="video-menu-wrap video-speed-wrap">
                    <button id="video-speed-button" class="video-pill-button video-speed-button" type="button" aria-controls="video-settings-panel" aria-expanded="false">1x</button>
                </div>

                <button id="video-picture-button" class="video-pill-button video-picture-button" type="button" aria-label="Picture and subtitle settings" aria-expanded="false" aria-controls="video-settings-panel" title="Picture and subtitle settings">
                    <RatioIcon size={15} aria-hidden="true" />
                </button>

                <button id="video-fullscreen" class="video-icon-btn video-fullscreen-btn" type="button" data-state="windowed" aria-label="Enter fullscreen" title="Enter fullscreen">
                    <MaximizeIcon class="video-fullscreen-symbol video-symbol-fullscreen-enter" aria-hidden="true" />
                    <MinimizeIcon class="video-fullscreen-symbol video-symbol-fullscreen-exit" aria-hidden="true" />
                </button>
            </div>
        </div>
    </div>
</div>
