import { describe, it, expect } from "vitest";
import {
    formatAppErrorDiagnostic,
    humanizeBackendError,
    toAppError,
} from "./errors";

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
describe("AppError", () => {
    it("classifies a stable operation code through an Error cause without losing it", () => {
        const operationError = {
            code: "deadline_exceeded",
            message: "backend wording callers must not parse",
        };
        const gatewayError = new Error("Backend invocation failed", { cause: operationError });

        const error = toAppError(gatewayError, { source: "backend" });

        expect(error).toMatchObject({
            kind: "timeout",
            source: "backend",
            code: "deadline_exceeded",
            retryable: true,
            message: "The request took too long. Try again.",
        });
        expect(error.cause).toBe(gatewayError);
    });

    it("never stringifies an arbitrary thrown object into user copy", () => {
        const error = toAppError({ privatePayload: "do not display" });

        expect(error.message).toBe("Something went wrong. Try again.");
        expect(error.details).toEqual({ name: "UnknownError" });
        expect(error.message).not.toContain("[object Object]");
    });

    it("redacts diagnostics while retaining useful structured fields", () => {
        const cause = new Error("token=super-secret failed at /Users/alice/TDrive/cache.db");
        cause.stack = "Error: token=super-secret\n    at open (/Users/alice/TDrive/src/open.ts:12:4)";
        Object.assign(cause, { code: "network_unavailable" });

        const error = toAppError(cause, { source: "backend" });
        const diagnostic = formatAppErrorDiagnostic(error);

        expect(diagnostic).toContain("Code: network_unavailable");
        expect(diagnostic).toContain("token=[redacted]");
        expect(diagnostic).toContain("[local path]");
        expect(diagnostic).not.toContain("super-secret");
        expect(diagnostic).not.toContain("/Users/alice");
    });

    it("keeps startup implementation details out of the recovery message", () => {
        const error = toAppError(new Error("binding crashed with password=hunter2"), {
            source: "startup",
        });

        expect(error).toMatchObject({
            kind: "unavailable",
            title: "TDrive could not start",
            retryable: true,
            message: "TDrive could not finish starting. Reload the app and try again.",
        });
        expect(error.details.message).toContain("password=[redacted]");
        expect(error.details.message).not.toContain("hunter2");
    });
});
