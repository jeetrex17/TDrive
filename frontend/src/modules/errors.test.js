import { describe, it, expect } from "vitest";
import { humanizeBackendError } from "./errors";

describe("humanizeBackendError", () => {
    it("strips the Error: prefix", () => {
        expect(humanizeBackendError("Error: something broke")).toBe("something broke");
    });
    it("maps cycle errors to plain language", () => {
        expect(humanizeBackendError("Error: projection: move would create cycle"))
            .toBe("Can't move a folder into itself or one of its subfolders.");
    });
    it("maps tg/network errors", () => {
        expect(humanizeBackendError("tg client not ready"))
            .toBe("Telegram is not reachable right now. Try again.");
    });
    it("passes through the uploader-permission message", () => {
        const msg = "Only the uploader can move this file in a shared drive";
        expect(humanizeBackendError(msg)).toBe(msg);
    });
    it("falls back for empty input", () => {
        expect(humanizeBackendError("")).toBe("Something went wrong.");
    });
    it("reads Error.message", () => {
        expect(humanizeBackendError(new Error("Error: nope"))).toBe("nope");
    });
});
