import { afterEach, describe, expect, it, vi } from "vitest";
import {
    DEFAULT_PLAYBACK_PREFERENCES,
    htmlPictureStyle,
    loadPlaybackPreferences,
    nativePreferenceCommands,
    normalizePlaybackPreferences,
    savePlaybackPreferences,
    subtitleBackgroundColors,
} from "./playback-preferences";

afterEach(() => vi.unstubAllGlobals());

describe("playback preferences", () => {
    it("defaults missing and malformed preferences without sharing mutable state", () => {
        for (const value of [null, [], "fit", undefined, { pictureMode: "javascript", subtitleColor: "red;bad" }]) {
            expect(normalizePlaybackPreferences(value)).toEqual(DEFAULT_PLAYBACK_PREFERENCES);
        }
        expect(normalizePlaybackPreferences(null)).not.toBe(DEFAULT_PLAYBACK_PREFERENCES);
    });

    it("normalizes safe bounds and never coerces untrusted values", () => {
        expect(normalizePlaybackPreferences({
            pictureMode: "4:3", subtitleFontSize: 999, subtitleColor: "#aaff00",
            subtitleOutlineSize: -4, subtitleBackground: true, overrideStyledSubtitles: true,
        })).toEqual({
            ...DEFAULT_PLAYBACK_PREFERENCES,
            pictureMode: "4:3", subtitleFontSize: 72, subtitleColor: "#AAFF00",
            subtitleOutlineSize: 0, subtitleBackground: true, overrideStyledSubtitles: true,
        });
        expect(normalizePlaybackPreferences({ subtitleFontSize: NaN, subtitleOutlineSize: Infinity,
            subtitleBackground: "false", overrideStyledSubtitles: 1 })).toEqual(DEFAULT_PLAYBACK_PREFERENCES);
        expect(normalizePlaybackPreferences({ subtitleFontSize: "24", subtitleColor: "#FFFFFFFF" })).toEqual(DEFAULT_PLAYBACK_PREFERENCES);
    });

    it("persists only validated appearance preferences and tolerates unavailable storage", () => {
        const storage = new Map<string, string>();
        vi.stubGlobal("localStorage", {
            getItem: (key: string) => storage.get(key) ?? null,
            setItem: (key: string, value: string) => storage.set(key, value),
        });
        expect(loadPlaybackPreferences()).toEqual(DEFAULT_PLAYBACK_PREFERENCES);
        savePlaybackPreferences({ ...DEFAULT_PLAYBACK_PREFERENCES, pictureMode: "fill", subtitleFontSize: 52 });
        expect(loadPlaybackPreferences().pictureMode).toBe("fill");
        expect(loadPlaybackPreferences().subtitleFontSize).toBe(52);
        expect(storage.size).toBe(1);
        const key = [...storage.keys()][0];
        storage.set(key, "{invalid");
        expect(loadPlaybackPreferences()).toEqual(DEFAULT_PLAYBACK_PREFERENCES);
        vi.stubGlobal("localStorage", { getItem: () => { throw new Error("denied"); }, setItem: () => { throw new Error("denied"); } });
        expect(loadPlaybackPreferences()).toEqual(DEFAULT_PLAYBACK_PREFERENCES);
        expect(() => savePlaybackPreferences(DEFAULT_PLAYBACK_PREFERENCES)).not.toThrow();
    });

    it("loads legacy backgrounds with safe color and transparency defaults", () => {
        expect(normalizePlaybackPreferences({ subtitleBackground: true })).toEqual({
            ...DEFAULT_PLAYBACK_PREFERENCES, subtitleBackground: true,
            subtitleBackgroundColor: "#000000", subtitleBackgroundTransparency: 31,
        });
        for (const value of [NaN, Infinity, "50", null]) {
            expect(normalizePlaybackPreferences({ subtitleBackgroundTransparency: value }).subtitleBackgroundTransparency).toBe(31);
        }
        for (const value of ["red", "#FFF", "#FFFFFFFF", "#000000;bad", 42]) {
            expect(normalizePlaybackPreferences({ subtitleBackgroundColor: value }).subtitleBackgroundColor).toBe("#000000");
        }
        expect(normalizePlaybackPreferences({ subtitleBackgroundColor: "#aabbcc", subtitleBackgroundTransparency: -2 })).toMatchObject({ subtitleBackgroundColor: "#AABBCC", subtitleBackgroundTransparency: 0 });
        expect(normalizePlaybackPreferences({ subtitleBackgroundTransparency: 102 }).subtitleBackgroundTransparency).toBe(100);
    });

    it.each([[0, "FF"], [50, "80"], [100, "00"], [31, "B0"]])("matches preview and native alpha at %s percent transparency", (transparency, alpha) => {
        const prefs = { ...DEFAULT_PLAYBACK_PREFERENCES, subtitleBackground: true, subtitleBackgroundColor: "#123456", subtitleBackgroundTransparency: transparency };
        expect(subtitleBackgroundColors(prefs)).toEqual({ native: `#${alpha}123456`, css: `#123456${alpha}` });
        expect(nativePreferenceCommands(prefs)).toContainEqual(["set", "sub-back-color", `#${alpha}123456`]);
        expect(subtitleBackgroundColors({ ...prefs, subtitleBackground: false })).toEqual({ native: "#00000000", css: "#00000000" });
    });

    it("persists custom background appearance", () => {
        let saved = "null";
        vi.stubGlobal("localStorage", { getItem: () => saved, setItem: (_key: string, value: string) => { saved = value; } });
        const prefs = { ...DEFAULT_PLAYBACK_PREFERENCES, subtitleBackground: true, subtitleBackgroundColor: "#12ABCD", subtitleBackgroundTransparency: 50 };
        savePlaybackPreferences(prefs);
        expect(loadPlaybackPreferences()).toEqual(prefs);
    });

    it("resets every native picture property when switching modes", () => {
        const expected = {
            fit: ["no", "0", "no"], fill: ["no", "1", "no"], original: ["downscale-big", "0", "no"],
            "16:9": ["no", "0", "16:9"], "4:3": ["no", "0", "4:3"],
        } as const;
        for (const [mode, values] of Object.entries(expected)) {
            const commands = nativePreferenceCommands(normalizePlaybackPreferences({ pictureMode: mode }));
            expect(commands.slice(0, 3)).toEqual([
                ["set", "video-unscaled", values[0]], ["set", "panscan", values[1]], ["set", "video-aspect-override", values[2]],
            ]);
        }
    });

    it("keeps authored styles by default and makes subtitle overrides explicit", () => {
        const commands = nativePreferenceCommands(DEFAULT_PLAYBACK_PREFERENCES);
        expect(commands).toContainEqual(["set", "sub-ass-override", "scale"]);
        expect(commands).toContainEqual(["set", "sub-font-size", "38"]);
        expect(commands).toContainEqual(["set", "sub-back-color", "#00000000"]);
        expect(commands).toContainEqual(["set", "sub-border-style", "outline-and-shadow"]);
        const custom = nativePreferenceCommands({ ...DEFAULT_PLAYBACK_PREFERENCES, subtitleBackground: true, overrideStyledSubtitles: true });
        expect(custom).toContainEqual(["set", "sub-back-color", "#B0000000"]);
        expect(custom).toContainEqual(["set", "sub-border-style", "background-box"]);
        expect(custom).toContainEqual(["set", "sub-ass-override", "strip"]);
        expect(custom).toContainEqual(["set", "sub-color", "#FFFFFF"]);
        expect(custom).toContainEqual(["set", "sub-outline-size", "1.65"]);
    });
});

describe("HTML picture layout", () => {
    it("fits and fills the available area and bounds original size", () => {
        expect(htmlPictureStyle("fit", 1000, 600, 1920, 1080)).toEqual({ width: "100%", height: "100%", objectFit: "contain" });
        expect(htmlPictureStyle("fill", 1000, 600, 1920, 1080).objectFit).toBe("cover");
        expect(htmlPictureStyle("original", 1000, 600, 320, 240).objectFit).toBe("scale-down");
    });

    it("fits an explicitly stretched ratio inside wide and tall viewports", () => {
        expect(htmlPictureStyle("4:3", 1000, 600, 1920, 1080)).toEqual({ width: "800px", height: "600px", objectFit: "fill" });
        expect(htmlPictureStyle("16:9", 640, 900, 640, 480)).toEqual({ width: "640px", height: "360px", objectFit: "fill" });
    });

    it("handles hidden or unmeasured video containers safely", () => {
        expect(htmlPictureStyle("16:9", 0, 0, 0, 0).objectFit).toBe("contain");
        expect(htmlPictureStyle("4:3", Infinity, 100, 0, 0).objectFit).toBe("contain");
    });
});
