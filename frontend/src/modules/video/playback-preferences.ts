export type PictureMode = "fit" | "fill" | "original" | "16:9" | "4:3";

export interface PlaybackPreferences {
    pictureMode: PictureMode;
    subtitleFontSize: number;
    subtitleColor: string;
    subtitleOutlineSize: number;
    subtitleBackground: boolean;
    overrideStyledSubtitles: boolean;
}

export const DEFAULT_PLAYBACK_PREFERENCES: Readonly<PlaybackPreferences> = Object.freeze({
    pictureMode: "fit",
    subtitleFontSize: 38,
    subtitleColor: "#FFFFFF",
    subtitleOutlineSize: 1.65,
    subtitleBackground: false,
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
        ["set", "sub-back-color", prefs.subtitleBackground ? "#AF000000" : "#00000000"],
        ["set", "sub-border-style", prefs.subtitleBackground ? "background-box" : "outline-and-shadow"],
        ["set", "sub-ass-override", prefs.overrideStyledSubtitles ? "force" : "scale"],
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
