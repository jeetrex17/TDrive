import { describe, it, expect, beforeEach } from "vitest";
import { formatRelative, uploaderChipLabel } from "./uploaders";
import { state } from "../state";

describe("formatRelative", () => {
    const now = Math.floor(Date.now() / 1000);
    it("returns empty for invalid or zero", () => {
        expect(formatRelative(0)).toBe("");
        expect(formatRelative(NaN)).toBe("");
    });
    it("bucketizes into relative spans", () => {
        expect(formatRelative(now - 5)).toBe("just now");
        expect(formatRelative(now - 120)).toBe("2m ago");
        expect(formatRelative(now - 3 * 3600)).toBe("3h ago");
    });
});

describe("uploaderChipLabel", () => {
    beforeEach(() => {
        state.activeChannel = { kind: "shared" };
        state.userNames = new Map();
    });

    it("returns null outside shared drives", () => {
        state.activeChannel = { kind: "personal" };
        expect(uploaderChipLabel({ uploaderID: 5, uploadTime: 0 })).toBeNull();
    });
    it("returns null when uploader id is missing", () => {
        expect(uploaderChipLabel({ uploaderID: 0 })).toBeNull();
    });
    it("returns null when the name is not yet resolved", () => {
        expect(uploaderChipLabel({ uploaderID: 5 })).toBeNull();
    });
    it("returns the resolved display label", () => {
        state.userNames.set("5", "A<b>");
        const label = uploaderChipLabel({ uploaderID: 5, uploadTime: 0 });
        expect(label).toContain("A<b>");
    });
});
