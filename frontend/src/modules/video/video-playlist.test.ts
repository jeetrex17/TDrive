import { describe, expect, it, vi } from "vitest";
import type { FileListFileRow } from "../../ui/file-list/types";
import {
    AUTO_NEXT_STORAGE_KEY,
    DEFAULT_AUTO_NEXT,
    deriveVideoPlaylist,
    loadAutoNextPreference,
    normalizeAutoNextPreference,
    saveAutoNextPreference,
    type AutoNextStorage,
} from "./video-playlist";

function makeRow(overrides: Partial<FileListFileRow> = {}): FileListFileRow {
    return {
        kind: "file",
        key: "file:fs:1",
        selectionKey: "file:1",
        id: "1",
        name: "clip.mp4",
        baseName: "clip",
        ext: "MP4",
        source: "fs",
        parentId: "folder",
        size: 128,
        metaLabel: "Today",
        sizeLabel: "128 B",
        ariaLabel: "File: clip.mp4",
        uploaderID: 0,
        uploadTime: 0,
        encrypted: false,
        canDelete: true,
        canRename: true,
        actions: [],
        ...overrides,
    };
}

describe("video playlist", () => {
    it("filters visible rows without changing their existing order", () => {
        const rows = [
            makeRow({ key: "file:fs:1", id: "1", name: "zulu.mkv" }),
            makeRow({ key: "file:fs:2", id: "2", name: "cover.jpg" }),
            makeRow({ key: "file:fs:3", id: "3", name: "alpha.MP4" }),
            makeRow({ key: "file:fs:4", id: "4", name: "middle.txt" }),
            makeRow({ key: "file:fs:5", id: "5", name: "omega.MOV" }),
        ];

        const playlist = deriveVideoPlaylist(rows, "file:fs:3");

        expect(playlist.items.map((item) => item.name)).toEqual([
            "zulu.mkv",
            "alpha.MP4",
            "omega.MOV",
        ]);
        expect(playlist.currentIndex).toBe(1);
        expect(rows.map((row) => row.name)).toEqual([
            "zulu.mkv",
            "cover.jpg",
            "alpha.MP4",
            "middle.txt",
            "omega.MOV",
        ]);
    });

    it("matches stable row keys and rejects ambiguous id fallbacks", () => {
        const items = [
            makeRow({ key: "file:fs:7", id: "7", name: "local.mp4" }),
            makeRow({ key: "file:tg:7", id: "7", name: "remote.mp4", source: "tg" }),
            makeRow({ key: "file:fs:8", id: "8", name: "unique.mp4" }),
        ];

        expect(deriveVideoPlaylist(items, "file:tg:7").currentIndex).toBe(1);
        expect(deriveVideoPlaylist(items, "7").currentIndex).toBe(-1);
        expect(deriveVideoPlaylist(items, 8).currentIndex).toBe(2);
        expect(deriveVideoPlaylist(items, "file:fs:missing").currentIndex).toBe(-1);
    });

    it("normalizes and persists auto-next through a safe storage boundary", () => {
        const storage = new Map<string, string>();
        const boundary: AutoNextStorage = {
            getItem: (key) => storage.get(key) ?? null,
            setItem: (key, value) => {
                storage.set(key, value);
            },
        };

        expect(normalizeAutoNextPreference(false)).toBe(false);
        expect(normalizeAutoNextPreference("false")).toBe(false);
        expect(normalizeAutoNextPreference("true")).toBe(true);
        expect(normalizeAutoNextPreference("malformed")).toBe(DEFAULT_AUTO_NEXT);
        expect(loadAutoNextPreference(boundary)).toBe(DEFAULT_AUTO_NEXT);

        saveAutoNextPreference(false, boundary);
        expect(storage.get(AUTO_NEXT_STORAGE_KEY)).toBe("false");
        expect(loadAutoNextPreference(boundary)).toBe(false);

        saveAutoNextPreference(true, boundary);
        expect(loadAutoNextPreference(boundary)).toBe(true);
    });

    it("falls back to enabled when storage is unavailable or throws", () => {
        const broken: AutoNextStorage = {
            getItem: vi.fn(() => {
                throw new Error("blocked");
            }),
            setItem: vi.fn(() => {
                throw new Error("blocked");
            }),
        };

        expect(loadAutoNextPreference(null)).toBe(DEFAULT_AUTO_NEXT);
        expect(loadAutoNextPreference(broken)).toBe(DEFAULT_AUTO_NEXT);
        expect(() => saveAutoNextPreference(false, broken)).not.toThrow();
        expect(() => loadAutoNextPreference()).not.toThrow();
        expect(() => saveAutoNextPreference(true)).not.toThrow();
    });
});
