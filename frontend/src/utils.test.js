import { describe, it, expect } from "vitest";
import { formatBytes, splitNameAndExt, formatDate, escapeHtml } from "./utils.js";

describe("formatBytes", () => {
    it("formats zero", () => expect(formatBytes(0)).toBe("0 B"));
    it("formats across units", () => {
        expect(formatBytes(512)).toBe("512 B");
        expect(formatBytes(1024)).toBe("1 KB");
        expect(formatBytes(1536)).toBe("1.5 KB");
        expect(formatBytes(1048576)).toBe("1 MB");
    });
});

describe("splitNameAndExt", () => {
    it("splits base and uppercased ext", () => {
        expect(splitNameAndExt("photo.png")).toEqual({ base: "photo", ext: "PNG" });
    });
    it("treats no extension as FILE", () => {
        expect(splitNameAndExt("README")).toEqual({ base: "README", ext: "FILE" });
    });
    it("treats dotfiles and trailing dots as FILE", () => {
        expect(splitNameAndExt(".env")).toEqual({ base: ".env", ext: "FILE" });
        expect(splitNameAndExt("a.")).toEqual({ base: "a.", ext: "FILE" });
    });
});

describe("escapeHtml", () => {
    it("escapes html-significant chars", () => {
        expect(escapeHtml(`<a href="x">&'`)).toBe("&lt;a href=&quot;x&quot;&gt;&amp;&#039;");
    });
    it("coerces nullish to empty string", () => {
        expect(escapeHtml(null)).toBe("");
        expect(escapeHtml(undefined)).toBe("");
    });
});

describe("formatDate", () => {
    it("returns a dash for falsy timestamps", () => expect(formatDate(0)).toBe("-"));
});
