import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { audioRenditionCount, prefersJsPlayer } from "./hls-source";

const isSupported = vi.hoisted(() => vi.fn(() => true));

vi.mock("hls.js", () => ({
    default: class {
        static isSupported = isSupported;
        static Events = {
            MANIFEST_PARSED: "manifest",
            AUDIO_TRACK_SWITCHED: "audio",
            SUBTITLE_TRACK_SWITCH: "subtitle",
            ERROR: "error",
        };
    },
}));

const MASTER = "http://127.0.0.1:1/media/hls/tok/index.m3u8";

function respondWith(body: string, ok = true) {
    vi.stubGlobal("fetch", vi.fn(async () => ({ ok, text: async () => body })));
}

beforeEach(() => {
    isSupported.mockReturnValue(true);
});

afterEach(() => {
    vi.unstubAllGlobals();
});

describe("counting what a master playlist offers", () => {
    it("counts one rendition per audio track", () => {
        respondWith([
            "#EXTM3U",
            '#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="Surround",DEFAULT=YES,URI="a0/index.m3u8"',
            '#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="Commentary",DEFAULT=NO,URI="a1/index.m3u8"',
            "#EXT-X-STREAM-INF:BANDWIDTH=1,AUDIO=\"audio\"",
            "v/index.m3u8",
        ].join("\n"));

        return expect(audioRenditionCount(MASTER)).resolves.toBe(2);
    });

    it("does not count a subtitle rendition as a soundtrack", async () => {
        respondWith([
            "#EXTM3U",
            '#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="English",URI="a0/index.m3u8"',
            '#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="English",URI="s0/index.m3u8"',
        ].join("\n"));

        expect(await audioRenditionCount(MASTER)).toBe(1);
    });

    it("reports nothing when the backend will not repackage the file", async () => {
        // The fetch is what forces the probe, so a container the remuxer
        // refuses answers with a status rather than a playlist.
        respondWith("this file cannot be repackaged for playback", false);

        expect(await audioRenditionCount(MASTER)).toBe(0);
    });

    it("reports nothing when the request fails outright", async () => {
        vi.stubGlobal("fetch", vi.fn(async () => { throw new Error("offline"); }));

        expect(await audioRenditionCount(MASTER)).toBe(0);
    });
});

describe("choosing the javascript player", () => {
    it("takes over only when there is a choice to offer", async () => {
        // One soundtrack has nothing to pick from, and Android plays the file
        // natively and faster, so the detour would cost that for nothing.
        respondWith('#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,NAME="English",URI="a0/index.m3u8"');
        expect(await prefersJsPlayer(MASTER)).toBe(false);

        respondWith([
            "#EXTM3U",
            '#EXT-X-MEDIA:TYPE=AUDIO,NAME="English",URI="a0/index.m3u8"',
            '#EXT-X-MEDIA:TYPE=AUDIO,NAME="Hindi",URI="a1/index.m3u8"',
        ].join("\n"));
        expect(await prefersJsPlayer(MASTER)).toBe(true);
    });

    it("stays out of the way when the file was never remuxable", async () => {
        respondWith("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nv/index.m3u8");

        expect(await prefersJsPlayer(MASTER)).toBe(false);
    });

    it("asks for nothing when there is no playlist to ask about", async () => {
        const fetched = vi.fn();
        vi.stubGlobal("fetch", fetched);

        expect(await prefersJsPlayer("")).toBe(false);
        expect(fetched).not.toHaveBeenCalled();
    });

    it("declines on a platform that cannot run it", async () => {
        isSupported.mockReturnValue(false);
        const fetched = vi.fn();
        vi.stubGlobal("fetch", fetched);

        expect(await prefersJsPlayer(MASTER)).toBe(false);
        expect(fetched).not.toHaveBeenCalled();
    });
});
