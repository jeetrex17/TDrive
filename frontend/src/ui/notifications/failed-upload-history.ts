import { get, readonly, writable } from 'svelte/store';
import { asRecord, boundedText } from '../../api/shared';
import { toAppError } from '../../modules/errors';
import type { UploadActivity, UploadActivityFile } from './upload-activity-store';

const STORAGE_PREFIX = 'tdrive.failed-uploads.v1.';
const SNAPSHOT_VERSION = 1;
const MAX_BATCHES = 30;
const MAX_FAILED_FILES = 500;
const MAX_TEXT_LENGTH = 300;
const MAX_SNAPSHOT_LENGTH = 1_500_000;
const ABSOLUTE_LOCATION = /^(?:[\\/]|~\/|[a-z]:[\\/]|[a-z][a-z\d+.-]*:\/\/|(?:blob|content|data|file|tg):)/i;
const ERROR_LOCATION = /(^|[\s"'(=])(?:[\\/]{1,2}|~\/|[a-z]:[\\/]|[a-z][a-z\d+.-]*:\/\/|(?:blob|content|file|tg):)[^\r\n]*/gi;

interface SavedActivity extends UploadActivity {
    failedFileCount: number;
}

const savedHistory = writable<SavedActivity[]>([]);
export const failedUploadHistory = readonly(savedHistory);
let activeAccountId = 0;

/** Switch to the signed-in account's display history. Zero detaches it. */
export function setFailedUploadHistoryAccount(accountId: number): void {
    const nextAccountId = count(accountId) ?? 0;
    if (activeAccountId === nextAccountId) return;
    activeAccountId = nextAccountId;
    savedHistory.set(nextAccountId ? readHistory(nextAccountId) : []);
}

/** Archive known failed rows when a batch finishes or is canceled. */
export function rememberFailedUploadActivity(entry: UploadActivity): void {
    if (!activeAccountId) return;
    const snapshot = savedActivity(entry, MAX_FAILED_FILES);
    if (!snapshot) return;
    const entries = boundedHistory([snapshot, ...get(savedHistory)]);
    try {
        const value = JSON.stringify({ version: SNAPSHOT_VERSION, accountId: activeAccountId, entries });
        localStorage.setItem(storageKey(activeAccountId), value);
    } catch {
        // A full or unavailable store must not hide failures in this session.
    }
    savedHistory.set(entries);
}

export function clearFailedUploadHistory(): void {
    if (!activeAccountId) return;
    try {
        localStorage.removeItem(storageKey(activeAccountId));
    } catch {
        // Clear the visible history even if the webview refuses storage writes.
    }
    savedHistory.set([]);
}

/** Explicit logout removes the local record and prevents later saves. */
export function clearAllFailedUploadHistory(): void {
    activeAccountId = 0;
    try {
        for (let index = localStorage.length - 1; index >= 0; index -= 1) {
            const key = localStorage.key(index);
            if (key?.startsWith(STORAGE_PREFIX)) localStorage.removeItem(key);
        }
    } catch {
        // A restricted webview can refuse even the storage lookup.
    }
    savedHistory.set([]);
}

function storageKey(accountId: number): string {
    return STORAGE_PREFIX + accountId;
}

function readHistory(accountId: number): SavedActivity[] {
    try {
        const value = localStorage.getItem(storageKey(accountId));
        if (!value || value.length > MAX_SNAPSHOT_LENGTH) return [];
        const { version, accountId: owner, entries } = asRecord(JSON.parse(value));
        if (version !== SNAPSHOT_VERSION || owner !== accountId || !Array.isArray(entries)) return [];
        return boundedHistory(entries);
    } catch {
        return [];
    }
}

function boundedHistory(entries: readonly unknown[]): SavedActivity[] {
    let remaining = MAX_FAILED_FILES;
    const result: SavedActivity[] = [];
    const seen = new Set<string>();
    for (const raw of entries) {
        if (!remaining || result.length >= MAX_BATCHES) break;
        const entry = savedActivity(raw, remaining);
        if (!entry || seen.has(entry.id)) continue;
        seen.add(entry.id);
        result.push(entry);
        remaining -= entry.files.length;
    }
    return result;
}

function savedActivity(value: unknown, limit: number): SavedActivity | null {
    const entry = asRecord(value);
    const id = text(entry.id);
    const driveId = entry.driveId;
    const status = entry.status;
    if ((status !== 'failed' && status !== 'canceled') || !id || typeof driveId !== 'number' ||
        !Number.isSafeInteger(driveId) || !driveId || !Array.isArray(entry.files)) return null;

    const files: UploadActivityFile[] = [];
    const indexes = new Set<number>();
    for (const rawFile of entry.files) {
        const file = asRecord(rawFile);
        const index = count(file.index);
        const name = text(file.name);
        if (file.status !== 'failed' || index === undefined || !name || ABSOLUTE_LOCATION.test(name) || indexes.has(index)) continue;
        indexes.add(index);
        if (files.length >= limit) continue;
        const path = relativePath(file.path);
        const size = count(file.size);
        const message = safeError(file.message);
        files.push({
            index, name, status: 'failed',
            ...(path ? { path } : {}),
            ...(size === undefined ? {} : { size }),
            ...(message ? { message } : {}),
        });
    }
    if (!files.length) return null;

    const total = count(entry.fileCount) ?? 0;
    const failedFileCount = Math.max(indexes.size, Math.min(total, count(entry.failedFileCount) ?? 0));
    const transferId = text(entry.transferId);
    const error = safeError(entry.error);
    return {
        id, driveId, name: text(entry.name) || 'Upload', driveTitle: text(entry.driveTitle),
        destination: text(entry.destination), encrypt: entry.encrypt === true,
        addedAt: count(entry.addedAt) ?? 0, startedAt: count(entry.startedAt) ?? 0,
        finishedAt: count(entry.finishedAt) ?? 0, status,
        files, fileCount: Math.max(total, failedFileCount), failedFileCount,
        ...(transferId ? { transferId } : {}),
        ...(error ? { error } : {}),
    };
}

function relativePath(value: unknown): string {
    const path = text(value);
    if (!path || path.includes('\\') ||
        path.split('/').some((segment) => !segment || segment === '.' || segment === '..' || ABSOLUTE_LOCATION.test(segment))) return '';
    return path;
}

function safeError(value: unknown): string {
    if (typeof value !== 'string' || !value) return '';
    // Error copy can contain a local path or a capability URL. Preserve its
    // public wording and use the existing diagnostic redaction for secrets.
    const message = value.replace(ERROR_LOCATION, '$1[location redacted]');
    if (message !== value) {
        const classified = toAppError(value, { source: 'backend' });
        if (classified.kind !== 'unexpected') {
            return text(classified.message.replace(ERROR_LOCATION, '$1[location redacted]'));
        }
    }
    return text(toAppError(message, { source: 'backend' }).details.message);
}

function text(value: unknown): string {
    return boundedText(value, MAX_TEXT_LENGTH);
}

function count(value: unknown): number | undefined {
    return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
}
