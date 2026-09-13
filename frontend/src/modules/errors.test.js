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
        expect(humanizeBackendError("")).toBe("Something went wrong. Try again.");
    });
    it("reads Error.message", () => {
        expect(humanizeBackendError(new Error("Error: nope"))).toBe("nope");
    });
    it("redacts credentials and local paths", () => {
        expect(humanizeBackendError("api_hash=abcdef failed at /Users/alice/TDrive/cache.db"))
            .toBe("api_hash=[redacted] failed at [local path]");
    });
    it("maps stable operation codes independently of display wording", () => {
        expect(humanizeBackendError({ code: "insufficient_storage", message: "localized detail" }))
            .toBe("There is not enough free disk space to finish this action.");
    });
    it("maps actionable backend failures without leaking backend vocabulary", () => {
        const cases = [
            ["file is already in this folder", "This item is already there."],
            ["invalid target directory", "Choose a valid destination folder."],
            ["record not found", "That item no longer exists. Refresh and try again."],
            ["encryption password required", "Enter your encryption password first."],
            ["PHONE_NUMBER_INVALID", "Enter a valid phone number, including the country code."],
            ["PHONE_CODE_INVALID", "That code was incorrect. Check it and try again."],
            ["PASSWORD_HASH_INVALID", "That two-step verification password was incorrect."],
            ["FLOOD_WAIT_30", "Telegram is temporarily limiting attempts. Wait a moment and try again."],
            ["AUTH_KEY_UNREGISTERED", "Your Telegram session expired. Sign in again."],
            ["permission denied", "You don't have permission to do that."],
            ["no space left on device", "There is not enough free disk space to finish this action."],
            ["deadline exceeded", "The request took too long. Try again."],
        ];
        for (const [raw, expected] of cases) {
            expect(humanizeBackendError(raw)).toBe(expected);
        }
    });
    it("caps unexpected backend detail", () => {
        expect(humanizeBackendError("x".repeat(500))).toHaveLength(240);
    });
});
