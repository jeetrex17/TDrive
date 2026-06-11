import { describe, it, expect, beforeEach } from "vitest";
import { formatRelative, uploaderChipHTML } from "./uploaders";
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

describe("uploaderChipHTML", () => {
    beforeEach(() => {
        state.activeChannel = { kind: "shared" };
        state.userNames = new Map();
    });

    it("returns null outside shared drives", () => {
        state.activeChannel = { kind: "personal" };
        expect(uploaderChipHTML({ uploaderID: 5, uploadTime: 0 })).toBeNull();
    });
    it("returns null when uploader id is missing", () => {
        expect(uploaderChipHTML({ uploaderID: 0 })).toBeNull();
    });
    it("returns null when the name is not yet resolved", () => {
        expect(uploaderChipHTML({ uploaderID: 5 })).toBeNull();
    });
    it("renders the resolved name, HTML-escaped", () => {
        state.userNames.set("5", "A<b>");
        const html = uploaderChipHTML({ uploaderID: 5, uploadTime: 0 });
        expect(html).toContain("A&lt;b&gt;");
    });
});
