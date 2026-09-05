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
    type SubtitleAppearance = Omit<PlaybackPreferences, 'pictureMode' | 'overrideStyledSubtitles'>;
    let subtitleDraft = $state<Partial<SubtitleAppearance>>({});
    let subtitlePreferences = $derived(normalizePlaybackPreferences({ ...preferences, ...subtitleDraft }));
    const modes: { value: PictureMode; label: string; detail: string }[] = [
        { value: 'fit', label: 'Fit', detail: 'Keep the original aspect ratio' },
        { value: 'fill', label: 'Fill', detail: 'Fill the frame, cropping the edges' },
        { value: 'original', label: 'Original size', detail: 'Avoid enlarging smaller videos' },
        { value: '16:9', label: '16:9', detail: 'Widescreen' },
        { value: '4:3', label: '4:3', detail: 'Classic television' },
    ];

    function updateSubtitle(change: Partial<SubtitleAppearance>) {
        subtitleDraft = { ...subtitleDraft, ...change };
    }

    function updatePicture(pictureMode: PictureMode) {
        onPreferencesChange(normalizePlaybackPreferences({ ...preferences, pictureMode }));
    }

    function saveSubtitles() {
        onPreferencesChange(normalizePlaybackPreferences({ ...subtitlePreferences, overrideStyledSubtitles: true }));
        subtitleDraft = {};
    }

    function resetSubtitles() {
        onPreferencesChange(normalizePlaybackPreferences({ ...DEFAULT_PLAYBACK_PREFERENCES, pictureMode: preferences.pictureMode }));
        subtitleDraft = {};
    }

</script>

<div id="video-picture-settings" class="video-settings-section" hidden>
    <h3>Picture</h3>
    <p class="video-settings-note">Choose how the video fits the frame.</p>
    <div class="video-picture-options" role="group" aria-label="Picture mode">
        {#each modes as mode (mode.value)}
            <button type="button" data-picture-mode={mode.value} class:is-selected={preferences.pictureMode === mode.value} aria-pressed={preferences.pictureMode === mode.value} onclick={() => updatePicture(mode.value)}>
                <span>{mode.label}</span><small>{mode.detail}</small>
            </button>
        {/each}
    </div>
</div>
<div id="video-subtitle-settings" class="video-settings-section" hidden>
    <div class="video-subtitle-appearance">
    <h3>Subtitle appearance</h3>
    <p id="video-subtitle-format-note" class="video-settings-note" role="status" hidden>This track uses image-based subtitles. Saved text styles won't change it.</p>
    <div class="video-subtitle-preview" aria-label="Subtitle preview" style:color={subtitlePreferences.subtitleColor} style:font-size={`${subtitlePreferences.subtitleFontSize * .55}px`} style:text-shadow={`0 1px ${subtitlePreferences.subtitleOutlineSize}px #000, 1px 0 ${subtitlePreferences.subtitleOutlineSize}px #000, -1px 0 ${subtitlePreferences.subtitleOutlineSize}px #000`}>
        <span style:background={subtitlePreferences.subtitleBackground ? '#000a' : 'transparent'}>Your next adventure awaits.</span>
    </div>
    <label class="video-settings-field" for="video-subtitle-size"><span>Size <output>{subtitlePreferences.subtitleFontSize}</output></span>
        <input id="video-subtitle-size" class="video-settings-range" style:--range-fill={`${(subtitlePreferences.subtitleFontSize - 20) / 52 * 100}%`} type="range" min="20" max="72" step="1" value={subtitlePreferences.subtitleFontSize} oninput={(event) => updateSubtitle({ subtitleFontSize: Number(event.currentTarget.value) })} />
    </label>
    <label class="video-settings-field video-settings-inline" for="video-subtitle-color"><span>Text color</span>
        <input id="video-subtitle-color" type="color" value={subtitlePreferences.subtitleColor} oninput={(event) => updateSubtitle({ subtitleColor: event.currentTarget.value })} />
    </label>
    <label class="video-settings-field" for="video-subtitle-outline"><span>Outline <output>{subtitlePreferences.subtitleOutlineSize}</output></span>
        <input id="video-subtitle-outline" class="video-settings-range" style:--range-fill={`${subtitlePreferences.subtitleOutlineSize / 6 * 100}%`} type="range" min="0" max="6" step="0.05" value={subtitlePreferences.subtitleOutlineSize} oninput={(event) => updateSubtitle({ subtitleOutlineSize: Number(event.currentTarget.value) })} />
    </label>
    <label class="video-settings-check"><input id="video-subtitle-background" type="checkbox" checked={subtitlePreferences.subtitleBackground} onchange={(event) => updateSubtitle({ subtitleBackground: event.currentTarget.checked })} /> Dark background</label>
    <div class="video-subtitle-actions">
        <button id="video-subtitle-save" class="video-settings-save" type="button" onclick={saveSubtitles}>Save</button>
        <button id="video-subtitle-reset" class="video-settings-reset" type="button" onclick={resetSubtitles}>Reset</button>
    </div>
    </div>
</div>
