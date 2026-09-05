export type PictureMode = "fit" | "fill" | "original" | "16:9" | "4:3";

export interface PlaybackPreferences {
    pictureMode: PictureMode;
    subtitleFontSize: number;
    subtitleColor: string;
    subtitleOutlineSize: number;
    subtitleBackground: boolean;
    subtitleBackgroundColor: string;
    subtitleBackgroundTransparency: number;
    overrideStyledSubtitles: boolean;
}

export const DEFAULT_PLAYBACK_PREFERENCES: Readonly<PlaybackPreferences> = Object.freeze({
    pictureMode: "fit",
    subtitleFontSize: 38,
    subtitleColor: "#FFFFFF",
    subtitleOutlineSize: 1.65,
    subtitleBackground: false,
    subtitleBackgroundColor: "#000000",
    subtitleBackgroundTransparency: 31,
    overrideStyledSubtitles: false,
});

const STORAGE_KEY = "tdrive.playback-appearance.v1";
const PICTURE_MODES: readonly PictureMode[] = ["fit", "fill", "original", "16:9", "4:3"];

function boundedNumber(value: unknown, fallback: number, min: number, max: number): number {
    return typeof value === "number" && Number.isFinite(value) ? Math.min(max, Math.max(min, value)) : fallback;
}

export function normalizePlaybackPreferences(value: unknown): PlaybackPreferences {
    const candidate = value && typeof value === "object" && !Array.isArray(value)
        ? value as Record<string, unknown> : {};
    return {
        pictureMode: PICTURE_MODES.includes(candidate.pictureMode as PictureMode)
            ? candidate.pictureMode as PictureMode : DEFAULT_PLAYBACK_PREFERENCES.pictureMode,
        subtitleFontSize: boundedNumber(candidate.subtitleFontSize, 38, 20, 72),
        subtitleColor: typeof candidate.subtitleColor === "string" && /^#[\da-f]{6}$/i.test(candidate.subtitleColor)
            ? candidate.subtitleColor.toUpperCase() : DEFAULT_PLAYBACK_PREFERENCES.subtitleColor,
        subtitleOutlineSize: boundedNumber(candidate.subtitleOutlineSize, 1.65, 0, 6),
        subtitleBackground: candidate.subtitleBackground === true,
        subtitleBackgroundColor: typeof candidate.subtitleBackgroundColor === "string" && /^#[\da-f]{6}$/i.test(candidate.subtitleBackgroundColor)
            ? candidate.subtitleBackgroundColor.toUpperCase() : DEFAULT_PLAYBACK_PREFERENCES.subtitleBackgroundColor,
        subtitleBackgroundTransparency: boundedNumber(candidate.subtitleBackgroundTransparency, DEFAULT_PLAYBACK_PREFERENCES.subtitleBackgroundTransparency, 0, 100),
        overrideStyledSubtitles: candidate.overrideStyledSubtitles === true,
    };
}

export function loadPlaybackPreferences(): PlaybackPreferences {
    try {
        return normalizePlaybackPreferences(JSON.parse(localStorage.getItem(STORAGE_KEY) ?? "null"));
    } catch {
        // Playback must remain usable when storage is unavailable or contains stale data.
        return { ...DEFAULT_PLAYBACK_PREFERENCES };
    }
}

export function savePlaybackPreferences(preferences: PlaybackPreferences): void {
    try {
        localStorage.setItem(STORAGE_KEY, JSON.stringify(normalizePlaybackPreferences(preferences)));
    } catch {
        // Appearance still applies to this session when browser storage is disabled.
    }
}

/** mpv places alpha first; CSS places it last. Share quantization for an identical preview. */
export function subtitleBackgroundColors(preferences: PlaybackPreferences): { native: string; css: string } {
    const prefs = normalizePlaybackPreferences(preferences);
    if (!prefs.subtitleBackground) return { native: "#00000000", css: "#00000000" };
    const alpha = Math.round(255 * (1 - prefs.subtitleBackgroundTransparency / 100)).toString(16).padStart(2, "0").toUpperCase();
    return { native: `#${alpha}${prefs.subtitleBackgroundColor.slice(1)}`, css: `${prefs.subtitleBackgroundColor}${alpha}` };
}

/** Reset all related properties, so changing modes never leaves a previous crop or stretch active. */
export function nativePreferenceCommands(preferences: PlaybackPreferences): string[][] {
    const prefs = normalizePlaybackPreferences(preferences);
    return [
        ["set", "video-unscaled", prefs.pictureMode === "original" ? "downscale-big" : "no"],
        ["set", "panscan", prefs.pictureMode === "fill" ? "1" : "0"],
        ["set", "video-aspect-override", prefs.pictureMode === "16:9" || prefs.pictureMode === "4:3" ? prefs.pictureMode : "no"],
        ["set", "sub-font-size", String(prefs.subtitleFontSize)],
        ["set", "sub-color", prefs.subtitleColor],
        ["set", "sub-outline-size", String(prefs.subtitleOutlineSize)],
        ["set", "sub-back-color", subtitleBackgroundColors(prefs).native],
        ["set", "sub-border-style", prefs.subtitleBackground ? "background-box" : "outline-and-shadow"],
        // Strip inline ASS/SRT formatting too: force still preserves inline size/color tags.
        ["set", "sub-ass-override", prefs.overrideStyledSubtitles ? "strip" : "scale"],
    ];
}

interface HtmlPictureStyle {
    width: string;
    height: string;
    objectFit: "contain" | "cover" | "fill" | "scale-down";
}

export function htmlPictureStyle(
    mode: PictureMode,
    containerWidth: number,
    containerHeight: number,
    _videoWidth: number,
    _videoHeight: number,
): HtmlPictureStyle {
    if (mode === "fill") return { width: "100%", height: "100%", objectFit: "cover" };
    if (mode === "original") return { width: "100%", height: "100%", objectFit: "scale-down" };
    if ((mode === "16:9" || mode === "4:3") && Number.isFinite(containerWidth) && Number.isFinite(containerHeight)
        && containerWidth > 0 && containerHeight > 0) {
        const ratio = mode === "16:9" ? 16 / 9 : 4 / 3;
        const width = Math.min(containerWidth, containerHeight * ratio);
        return { width: `${width}px`, height: `${width / ratio}px`, objectFit: "fill" };
    }
    return { width: "100%", height: "100%", objectFit: "contain" };
}
