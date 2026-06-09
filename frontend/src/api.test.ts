import { describe, it, expect } from "vitest";
import { backend, main } from "../wailsjs/go/models";
import { toFileItem, toFolderItem, toRootFile, toSearchHit } from "./api";

describe("api normalizers", () => {
    it("toFileItem maps snake_case to camelCase", () => {
        const f = backend.FileMetaData.createFrom({
            name: "a.txt", size: 10, msg_id: 5, parent_id: "root",
            upload_time: 100, uploader_id: 7, encrypted: true, plaintext_size: 8,
        });
        expect(toFileItem(f)).toEqual({
            msgId: 5, name: "a.txt", size: 10, parentId: "root",
            uploadTime: 100, uploaderId: 7, encrypted: true, plaintextSize: 8,
        });
    });

    it("toFileItem defaults missing fields", () => {
        const f = backend.FileMetaData.createFrom({ name: "x" });
        expect(toFileItem(f)).toMatchObject({
            name: "x", msgId: 0, uploaderId: 0, encrypted: false, plaintextSize: 0,
        });
    });

    it("toFolderItem maps fields", () => {
        const d = backend.Folder.createFrom({ id: "d1", name: "Docs", parent_id: "root" });
        expect(toFolderItem(d)).toEqual({ id: "d1", name: "Docs", parentId: "root" });
    });

    it("toRootFile maps id->msgId and access_hash->accessHash", () => {
        const f = main.TDriveFile.createFrom({ id: 9, name: "r", size: 3, access_hash: 42, date: 1 });
        expect(toRootFile(f)).toEqual({ msgId: 9, name: "r", size: 3, accessHash: 42, date: 1 });
    });

    it("toSearchHit clamps type to file|folder", () => {
        expect(toSearchHit(backend.SearchResult.createFrom({ type: "folder", id: "d" })).type).toBe("folder");
        expect(toSearchHit(backend.SearchResult.createFrom({ type: "file", id: "5" })).type).toBe("file");
        expect(toSearchHit(backend.SearchResult.createFrom({ type: "weird", id: "5" })).type).toBe("file");
    });
});
