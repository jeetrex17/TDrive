import { playbackResumeKey } from './playback-resume';

// The old hints lacked account and drive context, so they cannot be migrated.
const STORAGE_KEY = 'tdrive.video.nativeOnly.v2';
const MAX_ENTRIES = 200;

export interface NativeMemoryTarget {
    accountId: number;
    channelId: number;
    sourceIdentity?: string;
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

function targetKey(target: NativeMemoryTarget): string | null {
    const { accountId, channelId, id, sourceIdentity } = target;
    if (sourceIdentity && !sourceIdentity.startsWith(`source:${accountId}:`)) return null;
    const scopeKey = playbackResumeKey(accountId, { channelId, fileId: id }, sourceIdentity);
    // The format can change when a file is renamed or replaced.
    return scopeKey ? JSON.stringify([scopeKey, target.name, target.size ?? 0]) : null;
}

function load(source: NativeMemoryStorage): string[] {
    try {
        const parsed: unknown = JSON.parse(source.getItem(STORAGE_KEY) ?? '[]');
        return Array.isArray(parsed) ? parsed.slice(-MAX_ENTRIES).filter((entry): entry is string => typeof entry === 'string') : [];
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

export function prefersNativePlayer(target: NativeMemoryTarget | null, storage?: NativeMemoryStorage | null): boolean {
    const key = target && targetKey(target);
    const source = resolveStorage(storage);
    return source && key ? load(source).includes(key) : false;
}

/** Record a successful native fallback, keeping the newest hints when full. */
export function rememberNativePlayer(target: NativeMemoryTarget | null, storage?: NativeMemoryStorage | null): void {
    const key = target && targetKey(target);
    const source = resolveStorage(storage);
    if (!source || !key) return;
    const entries = load(source).filter((entry) => entry !== key);
    save(source, [...entries, key].slice(-MAX_ENTRIES));
}

export function forgetNativePlayer(target: NativeMemoryTarget | null, storage?: NativeMemoryStorage | null): void {
    const key = target && targetKey(target);
    const source = resolveStorage(storage);
    if (!source || !key) return;
    const entries = load(source);
    const remaining = entries.filter((entry) => entry !== key);
    if (remaining.length !== entries.length) save(source, remaining);
}
