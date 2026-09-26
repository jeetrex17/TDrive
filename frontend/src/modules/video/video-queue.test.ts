import { describe, expect, it } from "vitest";
import {
    createActivePlaylist,
    normalizeVideoTarget,
    playlistItemIdentity,
    playlistViewItems,
    type VideoOpenTarget,
} from "./video-queue";

function target(overrides: Partial<VideoOpenTarget> = {}): VideoOpenTarget {
    return { id: 10, name: "clip.mkv", ...overrides };
}

describe("normalizing a video target", () => {
    it("refuses a target with no usable file id, rather than opening a session that must fail", () => {
        expect(normalizeVideoTarget({ id: 0, name: "clip.mkv" })).toBeNull();
        expect(normalizeVideoTarget({ id: -3, name: "clip.mkv" })).toBeNull();
        expect(normalizeVideoTarget({ id: Number.NaN, name: "clip.mkv" })).toBeNull();
    });

    it("accepts an id that arrived as a string, because bindings hand it over as JSON", () => {
        expect(normalizeVideoTarget({ id: "42" as unknown as number, name: "clip.mkv" })?.id).toBe(42);
    });

    it("gives an unnamed file a title the bar can show", () => {
        expect(normalizeVideoTarget({ id: 1, name: "" })?.name).toBe("Video");
    });

    it("never carries a negative size through to the meta line", () => {
        expect(normalizeVideoTarget(target({ size: -5 }))?.size).toBe(0);
    });

    it("keeps a row key only when there is one, so a blank key never becomes an identity", () => {
        expect(normalizeVideoTarget(target({ key: "  " }))).not.toHaveProperty("key");
        expect(normalizeVideoTarget(target({ key: " row-7 " }))?.key).toBe("row-7");
    });
});

describe("building the active playlist", () => {
    const launchItems: VideoOpenTarget[] = [
        { id: 1, name: "a.mp4" },
        { id: 2, name: "b.mkv" },
        { id: 3, name: "c.mov" },
    ];

    it("plays the target alone when it was opened without a list", () => {
        const playlist = createActivePlaylist(target(), true);
        expect(playlist.items).toHaveLength(1);
        expect(playlist.currentIndex).toBe(0);
        expect(playlist.title).toBe("Videos");
    });

    it("takes the caller's index when it points at the file being opened", () => {
        const playlist = createActivePlaylist({ id: 2, name: "b.mkv" }, true, {
            items: launchItems,
            currentIndex: 1,
            title: "Downloads",
        });
        expect(playlist.currentIndex).toBe(1);
        expect(playlist.title).toBe("Downloads");
        expect(playlist.items).toHaveLength(3);
    });

    it("finds the file itself when the caller's index is stale, so the panel never highlights the wrong row", () => {
        const playlist = createActivePlaylist({ id: 3, name: "c.mov" }, true, {
            items: launchItems,
            currentIndex: 0,
            title: "Downloads",
        });
        expect(playlist.currentIndex).toBe(2);
    });

    it("falls back to a queue of one when the same file id appears twice and there is no way to tell them apart", () => {
        const playlist = createActivePlaylist({ id: 1, name: "a.mp4" }, true, {
            items: [{ id: 1, name: "a.mp4" }, { id: 1, name: "a copy.mp4" }],
            currentIndex: 9,
            title: "Downloads",
        });
        expect(playlist.items).toHaveLength(1);
        expect(playlist.title).toBe("Videos");
    });

    it("falls back to a queue of one when the opened file is not in the list at all", () => {
        const playlist = createActivePlaylist({ id: 99, name: "elsewhere.mp4" }, true, {
            items: launchItems,
            currentIndex: 0,
            title: "Downloads",
        });
        expect(playlist.items).toHaveLength(1);
        expect(playlist.items[0].id).toBe(99);
    });

    it("drops list entries with no usable id before deciding where the target sits", () => {
        const playlist = createActivePlaylist({ id: 3, name: "c.mov" }, true, {
            items: [{ id: 0, name: "broken" }, ...launchItems],
            currentIndex: 3,
            title: "Downloads",
        });
        expect(playlist.items).toHaveLength(3);
        expect(playlist.currentIndex).toBe(2);
    });

    it("prefers what the opener knew about the file over the list row it came from", () => {
        const playlist = createActivePlaylist({ id: 2, name: "b.mkv", size: 5120, encrypted: true }, true, {
            items: launchItems,
            currentIndex: 1,
            title: "Downloads",
        });
        expect(playlist.items[1].size).toBe(5120);
        expect(playlist.items[1].encrypted).toBe(true);
    });

    it("carries the auto-next preference it was given", () => {
        expect(createActivePlaylist(target(), false).autoNext).toBe(false);
        expect(createActivePlaylist(target(), true).autoNext).toBe(true);
    });
});

describe("playlist rows", () => {
    it("keys a row by the file list's own key when it has one", () => {
        expect(playlistItemIdentity({ id: 7, name: "a.mp4", key: "row-7" }, 0)).toBe("row-7");
    });

    it("keys duplicate files apart by position, so two rows never collapse into one", () => {
        expect(playlistItemIdentity({ id: 7, name: "a.mp4" }, 0)).toBe("video:7:0");
        expect(playlistItemIdentity({ id: 7, name: "a.mp4" }, 1)).toBe("video:7:1");
    });

    it("numbers the rows from one and labels each with its container", () => {
        const playlist = createActivePlaylist({ id: 1, name: "a.mp4" }, true, {
            items: [{ id: 1, name: "a.mp4" }, { id: 2, name: "b.mkv", size: 1024 }],
            currentIndex: 0,
            title: "Downloads",
        });
        const rows = playlistViewItems(playlist);
        expect(rows.map((row) => row.position)).toEqual([1, 2]);
        expect(rows[1].size).toBe(1024);
        expect(rows[1].format).toBeTruthy();
    });
});
