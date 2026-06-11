import { describe, it, expect, vi } from "vitest";

// Mock the generated Wails bindings so the gallery API functions can be tested
// without a live backend. Only the names api.ts imports need to exist.
vi.mock("../wailsjs/go/main/App", () => ({
    GetFolderContents: vi.fn(),
    GetFileList: vi.fn(),
    GetOrphanedFiles: vi.fn(),
    ListMedia: vi.fn(),
    Search: vi.fn(),
    Thumbnail: vi.fn(),
}));

import { getMedia, getThumbnail } from "./api";
import { ListMedia, Thumbnail } from "../wailsjs/go/main/App";

describe("getMedia", () => {
    it("maps ListMedia results to camelCase FileItems", async () => {
        (ListMedia as any).mockResolvedValue([
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
        (ListMedia as any).mockResolvedValue(null);
        expect(await getMedia()).toEqual([]);
    });
});

describe("getThumbnail", () => {
    it("builds a data URL from the payload", async () => {
        (Thumbnail as any).mockResolvedValue({ data_base64: "QUJD", mime_type: "image/jpeg" });
        expect(await getThumbnail(7)).toBe("data:image/jpeg;base64,QUJD");
    });

    it("rejects when the payload is empty", async () => {
        (Thumbnail as any).mockResolvedValue({ data_base64: "", mime_type: "" });
        await expect(getThumbnail(7)).rejects.toThrow();
    });
});
