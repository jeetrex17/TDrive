import { beforeEach, describe, it, expect, vi } from "vitest";

// Mock the generated Wails bindings so the gallery API functions can be tested
// without a live backend. Only the names api.ts imports need to exist.
vi.mock("../wailsjs/go/main/App", () => ({
    AttachNativeMedia: vi.fn(),
    CloseMedia: vi.fn(),
    CloseNativeMedia: vi.fn(),
    GetFolderContents: vi.fn(),
    GetFileList: vi.fn(),
    GetMediaStats: vi.fn(),
    HideNativeSeekThumbnail: vi.fn(),
    ListMedia: vi.fn(),
    MountDrive: vi.fn(),
    MountDrives: vi.fn(),
    MountStatus: vi.fn(),
    MoveNativeSeekThumbnail: vi.fn(),
    NativeMediaCommand: vi.fn(),
    OpenMedia: vi.fn(),
    OpenNativeMedia: vi.fn(),
    OpenStream: vi.fn(),
    ResizeNativeMedia: vi.fn(),
    Search: vi.fn(),
    ShowNativeSeekThumbnail: vi.fn(),
    Thumbnail: vi.fn(),
    UnmountDrive: vi.fn(),
    UpdateMediaPlayback: vi.fn(),
}));

import {
    attachNativeMedia,
    closeMedia,
    closeNativeMedia,
    getMedia,
    getMediaStats,
    getThumbnail,
    hideNativeSeekThumbnail,
    moveNativeSeekThumbnail,
    nativeMediaCommand,
    openMedia,
    openNativeMedia,
    openStream,
    resizeNativeMedia,
    showNativeSeekThumbnail,
    updateMediaPlayback,
} from "./api";
import {
    AttachNativeMedia,
    CloseMedia,
    CloseNativeMedia,
    GetMediaStats,
    HideNativeSeekThumbnail,
    ListMedia,
    MoveNativeSeekThumbnail,
    NativeMediaCommand,
    OpenMedia,
    OpenNativeMedia,
    OpenStream,
    ResizeNativeMedia,
    ShowNativeSeekThumbnail,
    Thumbnail,
    UpdateMediaPlayback,
} from "../wailsjs/go/main/App";

beforeEach(() => {
    vi.clearAllMocks();
});

function mockBackendResult<T>(mock: { mockResolvedValue(value: T): unknown }, value: unknown): void {
    mock.mockResolvedValue(value as T);
}

describe("getMedia", () => {
    it("maps ListMedia results to camelCase FileItems", async () => {
        mockBackendResult(vi.mocked(ListMedia), [
            {
                name: "p.jpg", size: 10, msg_id: 3, parent_id: "",
                upload_time: 5, uploader_id: 0, encrypted: false, plaintext_size: 0,
            },
        ]);
        expect(await getMedia()).toEqual([
            { msgId: 3, name: "p.jpg", size: 10, parentId: "", uploadTime: 5, uploaderId: 0, encrypted: false, plaintextSize: 0 },
        ]);
    });

    it("returns an empty list when ListMedia yields null", async () => {
        mockBackendResult(vi.mocked(ListMedia), null);
        expect(await getMedia()).toEqual([]);
    });
});

describe("getThumbnail", () => {
    it("builds a data URL from the payload", async () => {
        mockBackendResult(vi.mocked(Thumbnail), { data_base64: "QUJD", mime_type: "image/jpeg" });
        expect(await getThumbnail(7)).toBe("data:image/jpeg;base64,QUJD");
    });

    it("rejects when the payload is empty", async () => {
        mockBackendResult(vi.mocked(Thumbnail), { data_base64: "", mime_type: "" });
        await expect(getThumbnail(7)).rejects.toThrow();
    });
});

describe("native media API boundary", () => {
    const nativeSessionToken = "native-session-id";
    const attachSessionToken = "attach-session-id";
    const playerStateToken = "player-state-id";

    it("normalizes native open results and fills the initial state token", async () => {
        mockBackendResult(vi.mocked(OpenNativeMedia), {
            token: nativeSessionToken,
            thumbnail_url: `http://127.0.0.1/thumb/${nativeSessionToken}/10`,
            html_controls: true,
            presentation: "standalone",
            name: "movie.mkv",
            initial_state: {
                sequence: 3,
                status: "playing",
                paused: false,
                current_time: 12,
            },
            info: {
                channel_id: 7,
                file_id: 42,
                revision: 2,
                name: "movie.mkv",
                stored_size: 2048,
                plaintext_size: 4096,
                encrypted: true,
                multipart: true,
            },
        });

        await expect(openNativeMedia(42, { x: 1, y: 2, width: 640, height: 360 })).resolves.toEqual({
            token: nativeSessionToken,
            thumbnailUrl: `http://127.0.0.1/thumb/${nativeSessionToken}/10`,
            htmlControls: true,
            presentation: "standalone",
            name: "movie.mkv",
            initialState: {
                token: nativeSessionToken,
                sequence: 3,
                status: "playing",
                paused: false,
                current_time: 12,
            },
            info: {
                channelId: 7,
                fileId: 42,
                revision: 2,
                name: "movie.mkv",
                storedSize: 2048,
                plaintextSize: 4096,
                encrypted: true,
                multipart: true,
            },
        });
    });

    it("does not expose malformed native initial state payloads", async () => {
        mockBackendResult(vi.mocked(OpenNativeMedia), {
            token: nativeSessionToken,
            presentation: "embedded",
            initial_state: "not an object",
        });

        await expect(openNativeMedia(42, { x: 0, y: 0, width: 640, height: 360 })).resolves.toMatchObject({
            token: nativeSessionToken,
            presentation: "embedded",
            initialState: null,
        });
    });

    it("does not call native commands or close with empty tokens", async () => {
        await nativeMediaCommand("", ["set", "pause", "yes"]);
        await closeNativeMedia("");

        expect(NativeMediaCommand).not.toHaveBeenCalled();
        expect(CloseNativeMedia).not.toHaveBeenCalled();
    });

    it("rejects attach without a session token", async () => {
        await expect(attachNativeMedia("", { x: 0, y: 0, width: 640, height: 360 }))
            .rejects.toThrow("Media session is required");
        expect(AttachNativeMedia).not.toHaveBeenCalled();
    });

    it("normalizes attach results and preserves an authoritative state token", async () => {
        mockBackendResult(vi.mocked(AttachNativeMedia), {
            token: attachSessionToken,
            presentation: "unexpected",
            initial_state: { token: playerStateToken, status: "paused" },
            info: { name: "clip.avi" },
        });

        await expect(attachNativeMedia(attachSessionToken, { x: 4, y: 8, width: 320, height: 180 }))
            .resolves.toMatchObject({
                token: attachSessionToken,
                presentation: "embedded",
                initialState: { token: playerStateToken, status: "paused" },
                info: { name: "clip.avi" },
            });
    });

    it("forwards non-empty lifecycle and seek-overlay calls exactly once", async () => {
        const rect = { x: 10, y: 20, width: 192, height: 108 };

        await resizeNativeMedia("token", rect);
        await nativeMediaCommand("token", ["set", "pause", "yes"]);
        await showNativeSeekThumbnail("token", "YWJj", rect);
        await moveNativeSeekThumbnail("token", rect);
        await hideNativeSeekThumbnail("token");
        await closeNativeMedia("token");

        expect(ResizeNativeMedia).toHaveBeenCalledWith("token", rect);
        expect(NativeMediaCommand).toHaveBeenCalledWith("token", ["set", "pause", "yes"]);
        expect(ShowNativeSeekThumbnail).toHaveBeenCalledWith("token", "YWJj", rect);
        expect(MoveNativeSeekThumbnail).toHaveBeenCalledWith("token", rect);
        expect(HideNativeSeekThumbnail).toHaveBeenCalledWith("token");
        expect(CloseNativeMedia).toHaveBeenCalledWith("token");
    });

    it("drops incomplete lifecycle and seek-overlay calls", async () => {
        const rect = { x: 0, y: 0, width: 192, height: 108 };

        await resizeNativeMedia("", rect);
        await nativeMediaCommand("token", []);
        await showNativeSeekThumbnail("token", "", rect);
        await moveNativeSeekThumbnail("", rect);
        await hideNativeSeekThumbnail("");

        expect(ResizeNativeMedia).not.toHaveBeenCalled();
        expect(NativeMediaCommand).not.toHaveBeenCalled();
        expect(ShowNativeSeekThumbnail).not.toHaveBeenCalled();
        expect(MoveNativeSeekThumbnail).not.toHaveBeenCalled();
        expect(HideNativeSeekThumbnail).not.toHaveBeenCalled();
    });
});

describe("loopback media API boundary", () => {
    const loopbackSessionToken = "loopback-session-id";
    const opened = {
        token: loopbackSessionToken,
        url: `http://127.0.0.1/media/${loopbackSessionToken}`,
        thumbnail_url: `http://127.0.0.1/thumb/${loopbackSessionToken}`,
        name: "movie.mkv",
        kind: "video",
        mime_type: "video/x-matroska",
        supports_range: true,
        info: {
            channel_id: 5,
            file_id: 9,
            revision: 3,
            stored_size: 100,
            plaintext_size: 80,
            encrypted: true,
            multipart: false,
        },
    };

    it("normalizes both preview and stream open results", async () => {
        mockBackendResult(vi.mocked(OpenMedia), opened);
        mockBackendResult(vi.mocked(OpenStream), opened);

        const expected = expect.objectContaining({
            token: loopbackSessionToken,
            thumbnailUrl: opened.thumbnail_url,
            mimeType: "video/x-matroska",
            supportsRange: true,
            info: expect.objectContaining({ channelId: 5, fileId: 9, encrypted: true }),
        });
        await expect(openMedia(9)).resolves.toEqual(expected);
        await expect(openStream(9)).resolves.toEqual(expected);
    });

    it("closes and updates only active media sessions", async () => {
        await closeMedia("");
        await updateMediaPlayback({ token: "", currentTime: 1, duration: 2, bufferAhead: 3 });
        expect(CloseMedia).not.toHaveBeenCalled();
        expect(UpdateMediaPlayback).not.toHaveBeenCalled();

        await closeMedia(loopbackSessionToken);
        await updateMediaPlayback({ token: loopbackSessionToken, currentTime: 1, duration: 2, bufferAhead: 3 });
        expect(CloseMedia).toHaveBeenCalledWith(loopbackSessionToken);
        expect(UpdateMediaPlayback).toHaveBeenCalledWith({
            token: loopbackSessionToken,
            current_time: 1,
            duration: 2,
            buffer_ahead: 3,
        });
    });

    it("returns safe zero stats without a token and maps backend stats with one", async () => {
        await expect(getMediaStats("")).resolves.toEqual({
            playback: { bytesPerSecond: 0, recentFloodWait: false, lastFloodWaitSeconds: 0 },
            thumbnails: { bytesPerSecond: 0, recentFloodWait: false, lastFloodWaitSeconds: 0 },
        });
        expect(GetMediaStats).not.toHaveBeenCalled();

        mockBackendResult(vi.mocked(GetMediaStats), {
            playback: { bytes_per_second: 2048, recent_flood_wait: true, last_flood_wait_seconds: 4 },
            thumbnails: { bytes_per_second: 512, recent_flood_wait: false, last_flood_wait_seconds: 0 },
        });
        await expect(getMediaStats(loopbackSessionToken)).resolves.toEqual({
            playback: { bytesPerSecond: 2048, recentFloodWait: true, lastFloodWaitSeconds: 4 },
            thumbnails: { bytesPerSecond: 512, recentFloodWait: false, lastFloodWaitSeconds: 0 },
        });
    });
});
