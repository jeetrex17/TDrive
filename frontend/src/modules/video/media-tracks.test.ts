import { describe, expect, it } from "vitest";
import { normalizeNativeTracks, nativeTrackLabel, shortNativeTrackLabel } from "./media-tracks";

describe("native media tracks", () => {
    it("keeps valid audio and subtitle tracks without trusting bridge payloads", () => {
        expect(normalizeNativeTracks([
            {
                id: 2,
                type: "audio",
                title: " Director commentary ",
                language: "eng",
                codec: "aac",
                selected: true,
                default: false,
                forced: false,
            },
            {
                id: 4,
                type: "subtitle",
                language: "spa",
                codec: "ass",
                selected: false,
                default: true,
                forced: true,
            },
            { id: 0, type: "audio" },
            { id: 3.5, type: "audio" },
            { id: 5, type: "video" },
            { id: "6", type: "audio" },
            null,
        ])).toEqual([
            {
                id: 2,
                type: "audio",
                title: "Director commentary",
                language: "eng",
                codec: "aac",
                selected: true,
                default: false,
                forced: false,
            },
            {
                id: 4,
                type: "subtitle",
                title: undefined,
                language: "spa",
                codec: "ass",
                selected: false,
                default: true,
                forced: true,
            },
        ]);
        expect(normalizeNativeTracks({ tracks: [] })).toEqual([]);
    });

    it("builds concise labels from title, language, and codec with a useful fallback", () => {
        expect(nativeTrackLabel({
            id: 2,
            type: "audio",
            title: "Director commentary",
            language: "eng",
            codec: "aac",
            selected: false,
            default: false,
            forced: false,
        }, 0)).toBe("Director commentary / ENG / AAC");

        expect(nativeTrackLabel({
            id: 7,
            type: "subtitle",
            title: "",
            language: "",
            codec: "",
            selected: false,
            default: false,
            forced: false,
        }, 1)).toBe("Subtitle 2");
    });
});

describe("shortNativeTrackLabel", () => {
    const base = { id: 1, type: "audio" as const, selected: false, default: false, forced: false };

    it("prefers the language code, then a trimmed title", () => {
        expect(shortNativeTrackLabel({ ...base, language: "eng", title: "Main" }, 0)).toBe("ENG");
        expect(shortNativeTrackLabel({ ...base, title: "Signs" }, 0)).toBe("Signs");
        expect(shortNativeTrackLabel({ ...base, title: "Director commentary" }, 0)).toBe("Director co…");
    });

    it("says nothing when the file named nothing", () => {
        // The pill is a few dozen pixels on a phone. A label that only repeats
        // the pill's own position costs a line of the control row and tells the
        // viewer nothing, so the icon stands alone and the settings sheet
        // carries the full name.
        expect(shortNativeTrackLabel(base, 2)).toBe("");
        expect(shortNativeTrackLabel({ ...base, title: "Audio 1" }, 0)).toBe("");
        expect(shortNativeTrackLabel({ ...base, title: "Track 2" }, 1)).toBe("");
        expect(shortNativeTrackLabel({ ...base, title: "Subtitles" }, 0)).toBe("");
        // A real name is never mistaken for a generic one.
        expect(shortNativeTrackLabel({ ...base, title: "Audio description" }, 0)).toBe("Audio descr…");
    });
});
