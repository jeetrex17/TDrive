import { describe, it, expect } from "vitest";
import { backend, main } from "../wailsjs/go/models";
import {
    normalizeMountableDrives,
    normalizeMountStatus,
    toFileItem,
    toFolderItem,
    toRootFile,
    toSearchHit,
} from "./api";

describe("api normalizers", () => {
    it("normalizes mountable drives with personal first and rejects invalid rows", () => {
        expect(normalizeMountableDrives([
            { id: 22, title: "Project", kind: "shared" },
            { id: -1, title: "Invalid", kind: "shared" },
            { id: 11, title: "", kind: "personal" },
            { id: 22, title: "Duplicate", kind: "shared" },
            { id: 33, title: "Unknown", kind: "other" },
        ])).toEqual([
            { id: 11, title: "Personal", kind: "personal" },
            { id: 22, title: "Project", kind: "shared" },
        ]);
    });

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

    it("normalizes a capability-free mounted-drive view", () => {
        const normalized = normalizeMountStatus({
            phase: "mounted",
            mounted: true,
            mode: "read-only",
            label: "Tdrive personal",
            location: "/Volumes/Tdrive personal",
            error: "",
            drive: { id: 42, title: "Personal", kind: "personal" },
            url: "http://127.0.0.1:7777/tdrive-secret",
            commands: { mac_finder: "open http://127.0.0.1:7777/tdrive-secret" },
        });

        expect(normalized).toEqual({
            phase: "mounted",
            mounted: true,
            mode: "read-only",
            writeState: "disabled",
            acceptingWrites: false,
            activeWrites: 0,
            label: "Tdrive personal",
            location: "/Volumes/Tdrive personal",
            error: "",
            drive: { id: 42, title: "Personal", kind: "personal" },
        });
        expect(normalized).not.toHaveProperty("url");
        expect(normalized).not.toHaveProperty("commands");
    });

    it("normalizes writable lifecycle without trusting contradictory fields", () => {
        const ready = normalizeMountStatus({
            phase: "mounted",
            mounted: true,
            mode: "read-write",
            write_state: "ready",
            accepting_writes: true,
            active_writes: 2,
        });
        expect(ready).toMatchObject({
            mode: "read-write",
            writeState: "ready",
            acceptingWrites: true,
            activeWrites: 2,
        });

        const contradictory = normalizeMountStatus({
            mounted: true,
            mode: "read-only",
            write_state: "ready",
            accepting_writes: true,
            active_writes: Number.MAX_SAFE_INTEGER,
        });
        expect(contradictory).toMatchObject({
            mode: "read-only",
            writeState: "disabled",
            acceptingWrites: false,
            activeWrites: 0,
        });
    });

    it("rejects unsafe locations and endpoint-bearing error details", () => {
        const normalized = normalizeMountStatus({
            phase: "error",
            mounted: false,
            location: "http://127.0.0.1:9000/tdrive-deadbeef",
            error: "mount_webdav http://127.0.0.1:9000/tdrive-deadbeef failed",
            drive: { id: Number.MAX_SAFE_INTEGER + 1, title: "x", kind: "weird" },
        });

        expect(normalized.location).toBe("");
        expect(normalized.error).toBe("The drive could not be mounted. Try again.");
        expect(normalized.drive).toEqual({ id: 0, title: "x", kind: "unknown" });
        expect(normalized.label).toBe("Tdrive personal");
    });

    it("keeps bounded safe mount labels for shared drives", () => {
        expect(normalizeMountStatus({ label: "Tdrive - Family", mounted: true }).label).toBe("Tdrive - Family");
        expect(normalizeMountStatus({ label: "http://127.0.0.1:7777/tdrive-secret", mounted: true }).label).toBe("Tdrive personal");
    });

    it("repairs contradictory mount phases at the API boundary", () => {
        expect(normalizeMountStatus({ phase: "mounted", mounted: false }).phase).toBe("idle");
        expect(normalizeMountStatus({ phase: "disconnecting", mounted: false }).phase).toBe("disconnecting");
        expect(normalizeMountStatus({ phase: "mounting", mounted: false }).phase).toBe("mounting");
        expect(normalizeMountStatus({ phase: "idle", mounted: true }).phase).toBe("mounted");
        expect(normalizeMountStatus({ phase: "mounting", running: true }).mounted).toBe(false);
    });
});
