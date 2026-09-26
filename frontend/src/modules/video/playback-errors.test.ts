import { describe, expect, it } from "vitest";
import { errorDetail, errorMessage } from "./playback-errors";

describe("playback error text", () => {
    it("reads the message off an Error and stringifies anything else", () => {
        expect(errorDetail(new Error("session not found"))).toBe("session not found");
        expect(errorDetail("plain string failure")).toBe("plain string failure");
        // An Error with no message falls through to String(err), which is what
        // puts the bare word "Error" on screen rather than an empty message.
        expect(errorDetail(new Error(""))).toBe("Error");
        expect(errorDetail(null)).toBe("");
        expect(errorDetail(undefined)).toBe("");
    });

    it("names a lost Telegram connection instead of showing the gotd internals", () => {
        const reachTelegram = "Could not reach Telegram. Check your connection and try again.";
        expect(errorMessage(new Error("rpcDoRequest: resolve peer: PEER_ID_INVALID"), "fallback")).toBe(reachTelegram);
        expect(errorMessage(new Error("retryUntilAck: deadline exceeded"), "fallback")).toBe(reachTelegram);
        expect(errorMessage(new Error("engine forcibly closed"), "fallback")).toBe(reachTelegram);
    });

    it("recognises those failures whatever case the backend reported them in", () => {
        expect(errorMessage(new Error("RESOLVE PEER failed"), "fallback"))
            .toBe("Could not reach Telegram. Check your connection and try again.");
    });

    it("tells the reader a canceled request is just worth retrying", () => {
        expect(errorMessage(new Error("context canceled"), "fallback"))
            .toBe("The video request was canceled. Try again.");
    });

    it("keeps the caller's fallback for a failure it cannot name", () => {
        expect(errorMessage(new Error("mpv: no such decoder"), "Could not open this video. Try again."))
            .toBe("Could not open this video. Try again.");
        expect(errorMessage(undefined, "Could not open this video. Try again."))
            .toBe("Could not open this video. Try again.");
    });
});
