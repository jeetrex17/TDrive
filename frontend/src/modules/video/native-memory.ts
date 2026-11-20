/**
 * Remembers which files played after switching from the webview to native, so
 * the next open can start with the player that worked. Files are keyed by
 * message id, name and size: the same
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

function save(source: NativeMemoryStorage, entries: string[]): void {
    try {
        source.setItem(STORAGE_KEY, JSON.stringify(entries));
    } catch {
        // Storage failures should not interrupt a playback transition.
    }
}

/** prefersNativePlayer reports whether native playback has worked for target. */
export function prefersNativePlayer(target: NativeMemoryTarget, storage?: NativeMemoryStorage | null): boolean {
    const source = resolveStorage(storage);
    return source ? load(source).includes(targetKey(target)) : false;
}

/**
 * rememberNativePlayer records a successful native fallback for target. The
 * newest entries win when the list is full.
 */
export function rememberNativePlayer(target: NativeMemoryTarget, storage?: NativeMemoryStorage | null): void {
    const source = resolveStorage(storage);
    if (!source) return;
    const key = targetKey(target);
    const entries = load(source).filter((entry) => entry !== key);
    entries.push(key);
    save(source, entries.slice(-MAX_ENTRIES));
}

/** Let a direct-webview file retry there after its native route fails. */
export function forgetNativePlayer(target: NativeMemoryTarget, storage?: NativeMemoryStorage | null): void {
    const source = resolveStorage(storage);
    if (!source) return;
    const key = targetKey(target);
    const entries = load(source);
    const remaining = entries.filter((entry) => entry !== key);
    if (remaining.length !== entries.length) save(source, remaining);
}
