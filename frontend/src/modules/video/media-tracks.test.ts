import { describe, expect, it } from "vitest";
import { normalizeNativeTracks, nativeTrackLabel } from "./media-tracks";

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
