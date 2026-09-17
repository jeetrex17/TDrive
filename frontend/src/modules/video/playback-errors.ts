/**
 * Turning a backend failure into a sentence the reader can act on.
 *
 * The strings that come back from a failed media open are gotd and Go plumbing
 * -- "rpcDoRequest: resolve peer", "context canceled" -- and showing them puts
 * the reader in front of a fault they cannot diagnose and a Retry button they
 * cannot judge. This module is the one place that decides which raw failures
 * are recognisable enough to be named, so the video controller never has to
 * pattern-match on error text inline and the mapping stays testable without a
 * media session.
 */

/** The raw text of a failure, whatever shape the thrower used. */
export function errorDetail(err: unknown): string {
    return err instanceof Error && err.message ? err.message : String(err || "");
}

/**
 * The sentence to show for a failed open, or `fallback` when the failure is
 * not one we recognise.
 *
 * Only two classes of failure get their own wording, because only two are
 * things the reader can do something about: losing Telegram (check the
 * connection) and a request that was canceled out from under us (just try
 * again). Everything else keeps the caller's fallback, which is written for the
 * step that failed and says more than a generic guess would.
 */
export function errorMessage(err: unknown, fallback: string): string {
    const normalized = errorDetail(err).toLowerCase();
    if (
        normalized.includes("resolve peer") ||
        normalized.includes("rpcdorequest") ||
        normalized.includes("retryuntilack") ||
        normalized.includes("engine forcibly closed")
    ) {
        return "Could not reach Telegram. Check your connection and try again.";
    }
    if (normalized.includes("context canceled")) {
        return "The video request was canceled. Try again.";
    }
    return fallback;
}
