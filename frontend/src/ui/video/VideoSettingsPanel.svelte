<script lang="ts">
    import { DEFAULT_PLAYBACK_PREFERENCES, normalizePlaybackPreferences, type PlaybackPreferences, type PictureMode } from '../../modules/video/playback-preferences';

    let {
        initialPreferences = DEFAULT_PLAYBACK_PREFERENCES,
        onPreferencesChange = () => {},
    }: {
        initialPreferences?: PlaybackPreferences;
        onPreferencesChange?: (value: PlaybackPreferences) => void;
    } = $props();
    let preferences = $derived(normalizePlaybackPreferences(initialPreferences));
    const modes: { value: PictureMode; label: string; detail: string }[] = [
        { value: 'fit', label: 'Fit', detail: 'Keep the original aspect ratio' },
        { value: 'fill', label: 'Fill', detail: 'Fill the frame, cropping the edges' },
        { value: 'original', label: 'Original size', detail: 'Avoid enlarging smaller videos' },
        { value: '16:9', label: '16:9', detail: 'Widescreen' },
        { value: '4:3', label: '4:3', detail: 'Classic television' },
    ];

    // Editing appearance is an explicit request to replace embedded text styles.
    // Otherwise ASS/SSA tracks can silently ignore the size and color controls.
    function updateSubtitle(change: Partial<PlaybackPreferences>) {
        update({ ...change, overrideStyledSubtitles: true });
    }

    function update(change: Partial<PlaybackPreferences>) {
        const next = normalizePlaybackPreferences({ ...preferences, ...change });
        preferences = next;
        onPreferencesChange(next);
    }
</script>

<div id="video-picture-settings" class="video-settings-section" hidden>
    <h3>Picture</h3>
    <p class="video-settings-note">Choose how the video fits the frame.</p>
    <div class="video-picture-options" role="group" aria-label="Picture mode">
        {#each modes as mode (mode.value)}
            <button type="button" data-picture-mode={mode.value} class:is-selected={preferences.pictureMode === mode.value} aria-pressed={preferences.pictureMode === mode.value} onclick={() => update({ pictureMode: mode.value })}>
                <span>{mode.label}</span><small>{mode.detail}</small>
            </button>
        {/each}
    </div>
</div>
<div id="video-subtitle-settings" class="video-settings-section" hidden>
    <div class="video-subtitle-appearance">
    <h3>Subtitle appearance</h3>
    <div class="video-subtitle-preview" aria-label="Subtitle preview" style:color={preferences.subtitleColor} style:font-size={`${preferences.subtitleFontSize * .55}px`} style:text-shadow={`0 1px ${preferences.subtitleOutlineSize}px #000, 1px 0 ${preferences.subtitleOutlineSize}px #000, -1px 0 ${preferences.subtitleOutlineSize}px #000`}>
        <span style:background={preferences.subtitleBackground ? '#000a' : 'transparent'}>Your next adventure awaits.</span>
    </div>
    <label class="video-settings-field" for="video-subtitle-size"><span>Size <output>{preferences.subtitleFontSize}</output></span>
        <input id="video-subtitle-size" class="video-settings-range" style:--range-fill={`${(preferences.subtitleFontSize - 20) / 52 * 100}%`} type="range" min="20" max="72" step="1" value={preferences.subtitleFontSize} oninput={(event) => updateSubtitle({ subtitleFontSize: Number(event.currentTarget.value) })} />
    </label>
    <label class="video-settings-field video-settings-inline" for="video-subtitle-color"><span>Text color</span>
        <input id="video-subtitle-color" type="color" value={preferences.subtitleColor} oninput={(event) => updateSubtitle({ subtitleColor: event.currentTarget.value })} />
    </label>
    <label class="video-settings-field" for="video-subtitle-outline"><span>Outline <output>{preferences.subtitleOutlineSize}</output></span>
        <input id="video-subtitle-outline" class="video-settings-range" style:--range-fill={`${preferences.subtitleOutlineSize / 6 * 100}%`} type="range" min="0" max="6" step="0.05" value={preferences.subtitleOutlineSize} oninput={(event) => updateSubtitle({ subtitleOutlineSize: Number(event.currentTarget.value) })} />
    </label>
    <label class="video-settings-check"><input id="video-subtitle-background" type="checkbox" checked={preferences.subtitleBackground} onchange={(event) => updateSubtitle({ subtitleBackground: event.currentTarget.checked })} /> Dark background</label>
    <label class="video-settings-check"><input id="video-subtitle-override" type="checkbox" checked={preferences.overrideStyledSubtitles} onchange={(event) => update({ overrideStyledSubtitles: event.currentTarget.checked })} /> Override styled subtitles</label>
    <button id="video-subtitle-reset" class="video-settings-reset" type="button" onclick={() => update({ ...DEFAULT_PLAYBACK_PREFERENCES, pictureMode: preferences.pictureMode })}>Reset subtitle appearance</button>
    </div>
</div>
