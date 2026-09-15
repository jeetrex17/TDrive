/**
 * Remembers which files the webview player could not decode, so the next open
 * goes straight to the native player instead of paying for a failed HTML
 * attempt first. Files are keyed by message id, name and size: the same
 * message under a new name or size is a different upload.
 */
const STORAGE_KEY = 'tdrive.video.nativeOnly';
const MAX_ENTRIES = 200;

export interface NativeMemoryTarget {
    id: number;
    name: string;
    size?: number;
}

export type NativeMemoryStorage = Pick<Storage, 'getItem' | 'setItem'>;

function resolveStorage(storage?: NativeMemoryStorage | null): NativeMemoryStorage | null {
    if (storage !== undefined) return storage;
    try {
        return globalThis.localStorage ?? null;
    } catch {
        return null;
    }
}

function targetKey(target: NativeMemoryTarget): string {
    return `${target.id}:${target.name}:${target.size ?? 0}`;
}

function load(source: NativeMemoryStorage): string[] {
    try {
        const parsed: unknown = JSON.parse(source.getItem(STORAGE_KEY) ?? '[]');
        return Array.isArray(parsed) ? parsed.filter((entry): entry is string => typeof entry === 'string') : [];
    } catch {
        return [];
    }
}

/** prefersNativePlayer reports whether the webview already failed on target. */
export function prefersNativePlayer(target: NativeMemoryTarget, storage?: NativeMemoryStorage | null): boolean {
    const source = resolveStorage(storage);
    return source ? load(source).includes(targetKey(target)) : false;
}

/**
 * rememberNativePlayer records that the webview could not decode target. The
 * newest entries win when the list is full.
 */
export function rememberNativePlayer(target: NativeMemoryTarget, storage?: NativeMemoryStorage | null): void {
    const source = resolveStorage(storage);
    if (!source) return;
    const key = targetKey(target);
    const entries = load(source).filter((entry) => entry !== key);
    entries.push(key);
    try {
        source.setItem(STORAGE_KEY, JSON.stringify(entries.slice(-MAX_ENTRIES)));
    } catch {
        // Storage can be full or disabled; forgetting only costs one HTML attempt.
    }
}
