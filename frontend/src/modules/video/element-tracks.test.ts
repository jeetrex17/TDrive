import { describe, expect, it, vi } from "vitest";
import {
    elementAudioTracks,
    elementTextTracks,
    elementTracks,
    selectElementAudioTrack,
    selectElementTextTrack,
    watchElementTracks,
} from "./element-tracks";

// A stand-in for the live lists, which jsdom does not implement: it has no
// audioTracks at all, and its textTracks cannot be populated without real cues.
function trackList<T>(items: T[]) {
    const listeners = new Map<string, Set<() => void>>();
    const list = {
        length: items.length,
        addEventListener: vi.fn((type: string, listener: () => void) => {
            if (!listeners.has(type)) listeners.set(type, new Set());
            listeners.get(type)?.add(listener);
        }),
        removeEventListener: vi.fn((type: string, listener: () => void) => {
            listeners.get(type)?.delete(listener);
        }),
        fire(type: string) {
            for (const listener of listeners.get(type) ?? []) listener();
        },
    } as Record<string, unknown> & { length: number; fire(type: string): void };
    items.forEach((item, index) => { list[index] = item; });
    return list;
}

interface FakeList {
    fire(type: string): void;
}

// The element is returned alongside its lists so a test can fire an event at
// one without reaching back through a type the DOM does not admit to having.
function videoWith(audio: unknown[], text: unknown[]) {
    const audioTracks = trackList(audio);
    const textTracks = trackList(text);
    const video = { audioTracks, textTracks } as unknown as HTMLVideoElement;
    return Object.assign(video, {}) as HTMLVideoElement & { audioTracks: FakeList; textTracks: FakeList };
}

describe("reading the element's tracks", () => {
    it("reports the audio tracks the stream carries", () => {
        const video = videoWith([
            { label: "Surround", language: "eng", enabled: true },
            { label: "Commentary", language: "eng", enabled: false },
        ], []);

        expect(elementAudioTracks(video)).toEqual([
            { id: 1, type: "audio", title: "Surround", language: "eng", selected: true, default: true, forced: false },
            { id: 2, type: "audio", title: "Commentary", language: "eng", selected: false, default: false, forced: false },
        ]);
    });

    it("leaves out tracks nobody would choose", () => {
        // Metadata and chapter tracks are machinery. Offering them in a
        // subtitle menu is how a viewer ends up turning on an empty track and
        // concluding subtitles are broken.
        const video = videoWith([], [
            { kind: "subtitles", label: "English", language: "en", mode: "showing" },
            { kind: "metadata", label: "id3", mode: "hidden" },
            { kind: "chapters", label: "Chapters", mode: "hidden" },
        ]);

        const subtitles = elementTextTracks(video);
        expect(subtitles).toHaveLength(1);
        expect(subtitles[0]).toMatchObject({ id: 1, title: "English", selected: true });
    });

    it("says nothing when the platform has no track lists", () => {
        // Chromium does not implement audioTracks, so this is Android's normal
        // answer rather than a failure.
        expect(elementTracks({} as HTMLVideoElement)).toEqual([]);
    });
});

describe("choosing a track", () => {
    it("turns the others off in the same pass", () => {
        // A list may legally have several enabled at once, and a player handed
        // two live audio tracks mixes them together.
        const audio = [
            { label: "Surround", enabled: true },
            { label: "Commentary", enabled: true },
        ];
        const video = videoWith(audio, []);

        selectElementAudioTrack(video, 2);
        expect(audio.map((track) => track.enabled)).toEqual([false, true]);
    });

    it("turns subtitles off when nothing is chosen", () => {
        const text = [
            { kind: "subtitles", mode: "showing" },
            { kind: "subtitles", mode: "disabled" },
        ];
        const video = videoWith([], text);

        selectElementTextTrack(video, null);
        expect(text.map((track) => track.mode)).toEqual(["disabled", "disabled"]);

        selectElementTextTrack(video, 2);
        expect(text.map((track) => track.mode)).toEqual(["disabled", "showing"]);
    });

    it("does not disturb a metadata track on its way past", () => {
        const text = [
            { kind: "metadata", mode: "hidden" },
            { kind: "subtitles", mode: "disabled" },
        ];
        const video = videoWith([], text);

        selectElementTextTrack(video, 2);
        expect(text[0].mode).toBe("hidden");
        expect(text[1].mode).toBe("showing");
    });
});

describe("watching for tracks", () => {
    it("calls back when a list changes and stops when released", () => {
        // HLS tracks arrive after the playlist and the initialisation segment,
        // so the pills appear on this event rather than on the next state
        // change that happens to follow.
        const video = videoWith([{ enabled: true }], []);
        const onChange = vi.fn();

        const release = watchElementTracks(video, onChange);
        video.audioTracks.fire("addtrack");
        expect(onChange).toHaveBeenCalledOnce();

        release();
        video.audioTracks.fire("addtrack");
        expect(onChange).toHaveBeenCalledOnce();
    });

    it("survives a platform with no lists to watch", () => {
        expect(() => watchElementTracks({} as HTMLVideoElement, vi.fn())()).not.toThrow();
    });
});
