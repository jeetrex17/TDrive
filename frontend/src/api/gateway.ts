export type RuntimeUnsubscribe = () => void;

export const noopRuntimeUnsubscribe: RuntimeUnsubscribe = () => {};

/**
 * A generated Wails binding rejected. The original value is deliberately kept
 * intact for the error boundary to classify; this boundary never presents it.
 */
export class BackendInvocationError extends Error {
    readonly method: string;
    readonly cause: unknown;

    constructor(method: string, cause: unknown) {
        super(errorMessage(cause, "Backend request failed."));
        this.name = "BackendInvocationError";
        this.method = method;
        this.cause = cause;
    }
}

/** A native runtime capability was not injected into the current webview. */
export class RuntimeUnavailableError extends Error {
    readonly method: string;

    constructor(method: string) {
        super(`Native runtime method ${method} is unavailable.`);
        this.name = "RuntimeUnavailableError";
        this.method = method;
    }
}

/** A native runtime capability was available but rejected its invocation. */
export class RuntimeInvocationError extends Error {
    readonly method: string;
    readonly cause: unknown;

    constructor(method: string, cause: unknown) {
        super(errorMessage(cause, "Native runtime request failed."));
        this.name = "RuntimeInvocationError";
        this.method = method;
        this.cause = cause;
    }
}

/**
 * Converts the generated bridge's sync-or-Promise implementation into a
 * Promise while preserving its original rejection as `cause`.
 */
export async function invokeBackend<Args extends unknown[], Result>(
    binding: (...args: Args) => Result,
    ...args: Args
): Promise<Awaited<Result>> {
    try {
        const result = await binding(...args);
        return result as Awaited<Result>;
    } catch (cause) {
        throw new BackendInvocationError(binding.name || "bound backend method", cause);
    }
}

/** Invokes a synchronous native runtime method without losing its receiver. */
export function invokeRuntime<Args extends unknown[], Result>(
    method: string,
    target: object,
    binding: (...args: Args) => Result,
    ...args: Args
): Result {
    try {
        return binding.apply(target, args);
    } catch (cause) {
        throw new RuntimeInvocationError(method, cause);
    }
}

/** Normalizes a sync-or-Promise runtime result and preserves rejected causes. */
export async function invokeRuntimeAsync<Args extends unknown[], Result>(
    method: string,
    target: object,
    binding: (...args: Args) => Result,
    ...args: Args
): Promise<Awaited<Result>> {
    try {
        const result = await binding.apply(target, args);
        return result as Awaited<Result>;
    } catch (cause) {
        throw new RuntimeInvocationError(method, cause);
    }
}

function errorMessage(cause: unknown, fallback: string): string {
    if (typeof cause === "string") {
        const message = cause.trim();
        if (message) return message;
    }

    if (cause instanceof Error) {
        const message = cause.message.trim();
        if (message) return message;
    }

    if (cause !== null && typeof cause === "object" && "message" in cause) {
        const message = cause.message;
        if (typeof message === "string") {
            const normalized = message.trim();
            if (normalized) return normalized;
        }
    }

    return fallback;
}
