import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';
import type { UploadActivity, UploadActivityFile } from './upload-activity-store';
import {
    clearAllFailedUploadHistory,
    clearFailedUploadHistory,
    failedUploadHistory,
    rememberFailedUploadActivity,
    setFailedUploadHistoryAccount,
} from './failed-upload-history';

const storage = new Map<string, string>();
const setItem = vi.fn((key: string, value: string) => storage.set(key, value));
const removeItem = vi.fn((key: string) => storage.delete(key));

function batch(overrides: Partial<UploadActivity> = {}): UploadActivity {
    return {
        id: 'upload:batch-1', name: 'Album', driveId: -123, driveTitle: 'Team drive',
        destination: 'Team drive / Archive', encrypt: true, addedAt: 100, startedAt: 200,
        finishedAt: 300, status: 'failed', fileCount: 2, transferId: 'xfer:upload-1',
        resumableJobIds: ['durable-job'],
        files: [
            { index: 0, name: 'a.jpg', path: 'Album/a.jpg', status: 'done' },
            { index: 1, name: 'b.jpg', path: 'Album/2024/b.jpg', size: 2304, status: 'failed', message: 'The file changed during upload.' },
        ],
        ...overrides,
    };
}

beforeEach(() => {
    setFailedUploadHistoryAccount(0);
    storage.clear();
    setItem.mockClear();
    removeItem.mockClear();
    vi.stubGlobal('localStorage', {
        get length() { return storage.size; },
        key: (index: number) => [...storage.keys()][index] ?? null,
        getItem: (key: string) => storage.get(key) ?? null,
        setItem,
        removeItem,
    });
});

afterEach(() => {
    setFailedUploadHistoryAccount(0);
    vi.unstubAllGlobals();
});

describe('failed upload history', () => {
    it('restores a failed batch with its destination and display details', () => {
        const entry = batch();
        setFailedUploadHistoryAccount(42);
        rememberFailedUploadActivity(entry);
        setFailedUploadHistoryAccount(0);

        expect(get(failedUploadHistory)).toEqual([]);
        setFailedUploadHistoryAccount(42);

        // Keep the aggregate identity and count without retaining successful file details.
        const restored = get(failedUploadHistory);
        expect(restored).toEqual([{
            id: entry.id, name: entry.name, driveId: entry.driveId, driveTitle: entry.driveTitle,
            destination: entry.destination, encrypt: entry.encrypt, addedAt: entry.addedAt,
            startedAt: entry.startedAt, finishedAt: entry.finishedAt, status: entry.status,
            fileCount: 2, failedFileCount: 1, restored: true, transferId: entry.transferId, files: [entry.files[1]],
        }]);
        expect(setItem).toHaveBeenCalledTimes(1);
    });

    it('isolates histories across logout, account switch and clearing', () => {
        setFailedUploadHistoryAccount(42);
        rememberFailedUploadActivity(batch());
        setFailedUploadHistoryAccount(17);
        expect(get(failedUploadHistory)).toEqual([]);
        rememberFailedUploadActivity(batch({ id: 'upload:batch-2' }));
        setFailedUploadHistoryAccount(0);
        rememberFailedUploadActivity(batch({ id: 'anonymous' }));
        expect(get(failedUploadHistory)).toEqual([]);
        expect(setItem).toHaveBeenCalledTimes(2);

        setFailedUploadHistoryAccount(17);
        expect(get(failedUploadHistory).map(({ id }) => id)).toEqual(['upload:batch-2']);
        clearFailedUploadHistory();
        setFailedUploadHistoryAccount(42);
        expect(get(failedUploadHistory).map(({ id }) => id)).toEqual(['upload:batch-1']);
        setFailedUploadHistoryAccount(17);
        expect(get(failedUploadHistory)).toEqual([]);
        expect(removeItem).toHaveBeenCalledTimes(1);
    });

    it('archives settled failures only and replaces a repeated batch id', () => {
        setFailedUploadHistoryAccount(42);
        for (const status of ['queued', 'active', 'done'] as const) {
            rememberFailedUploadActivity(batch({ status }));
        }
        rememberFailedUploadActivity(batch({ status: 'canceled', files: [{ index: 0, name: 'a.jpg', status: 'canceled' }] }));
        rememberFailedUploadActivity(batch({ files: [{ index: 0, name: 'a.jpg', status: 'done' }] }));
        expect(setItem).not.toHaveBeenCalled();
        rememberFailedUploadActivity(batch({ finishedAt: 300 }));
        rememberFailedUploadActivity(batch({ finishedAt: 400 }));
        expect(get(failedUploadHistory)).toMatchObject([{ finishedAt: 400 }]);
    });

    it('retains known failures in a canceled batch', () => {
        setFailedUploadHistoryAccount(42);
        const entry = batch({ status: 'canceled' });
        rememberFailedUploadActivity(entry);
        setFailedUploadHistoryAccount(0);
        setFailedUploadHistoryAccount(42);
        expect(get(failedUploadHistory)).toMatchObject([{ status: 'canceled', failedFileCount: 1, files: [entry.files[1]] }]);
    });

    it('clears all account histories and detaches the account on full logout', () => {
        storage.set('unrelated', 'keep');
        for (const accountId of [42, 17]) {
            setFailedUploadHistoryAccount(accountId);
            rememberFailedUploadActivity(batch());
        }
        clearAllFailedUploadHistory();
        rememberFailedUploadActivity(batch());
        expect(get(failedUploadHistory)).toEqual([]);
        expect([...storage.keys()]).toEqual(['unrelated']);
    });

    it('omits source capabilities and redacts local paths and credentials from failure copy', () => {
        const unsafe = {
            ...batch({ error: 'Could not read /home/alex/private.txt, token=do-not-save' }),
            sourcePaths: ['/home/alex/source.png'],
            files: [{ ...batch().files[1], path: '/Volumes/Private/b.jpg', progress: 55, retry: 'picker-token' }],
        };
        setFailedUploadHistoryAccount(42);
        rememberFailedUploadActivity(unsafe);
        const [entry] = get(failedUploadHistory);
        const [file] = entry.files;
        expect(file).not.toHaveProperty('path');
        expect(file).not.toHaveProperty('progress');
        expect(file).not.toHaveProperty('retry');
        expect(entry.error).toContain('Could not read');
        const saved = [...storage.values()].join('');
        for (const privateText of ['/home/alex', 'do-not-save', 'Private/b.jpg', 'durable-job', 'picker-token']) {
            expect(saved).not.toContain(privateText);
        }
    });

    it('keeps the actionable reason when a filesystem error contains a path', () => {
        setFailedUploadHistoryAccount(42);
        rememberFailedUploadActivity(batch({ error: 'open /Volumes/Private Notes/file.jpg: permission denied' }));
        expect(get(failedUploadHistory)[0].error).toBe("You don't have permission to do that.");
        expect([...storage.values()].join('')).not.toContain('Private Notes');
    });

    it('keeps a colon in a display name or selection-relative folder', () => {
        const file = { ...batch().files[1], name: 'Report:2026.txt', path: 'Backup:2026/Report:2026.txt' };
        setFailedUploadHistoryAccount(42);
        rememberFailedUploadActivity(batch({ files: [file] }));
        setFailedUploadHistoryAccount(0);
        setFailedUploadHistoryAccount(42);

        expect(get(failedUploadHistory)).toMatchObject([{ files: [file] }]);
    });

    it('bounds failed files and keeps the newest batches', () => {
        setFailedUploadHistoryAccount(42);
        const files: UploadActivityFile[] = Array.from({ length: 600 }, (_, index) => ({
            index, name: `file-${index}.bin`, status: 'failed', message: 'x'.repeat(600),
        }));
        rememberFailedUploadActivity(batch({ files, fileCount: files.length }));
        rememberFailedUploadActivity(batch({ id: 'upload:batch-2' }));
        setFailedUploadHistoryAccount(0);
        setFailedUploadHistoryAccount(42);

        const entries = get(failedUploadHistory);
        expect(entries[0].id).toBe('upload:batch-2');
        expect(entries.flatMap((entry) => entry.files)).toHaveLength(500);
        expect(entries[1].files[0].message?.length).toBeLessThan(600);
        expect(entries[1].fileCount).toBe(600);
        expect(entries[1].failedFileCount).toBe(600);
    });

    it('rejects mismatched ownership and removes duplicate batch and file keys', () => {
        setFailedUploadHistoryAccount(42);
        rememberFailedUploadActivity(batch());
        const [key] = storage.keys();
        setFailedUploadHistoryAccount(0);
        storage.set(key, JSON.stringify({ version: 1, accountId: 17, entries: [batch()] }));
        setFailedUploadHistoryAccount(42);
        expect(get(failedUploadHistory)).toEqual([]);

        setFailedUploadHistoryAccount(0);
        const failure = batch().files[1];
        storage.set(key, JSON.stringify({
            version: 1, accountId: 42, entries: [null, batch({ files: [failure, failure] }), batch()],
        }));
        setFailedUploadHistoryAccount(42);
        expect(get(failedUploadHistory)).toMatchObject([{ failedFileCount: 1, files: [failure] }]);
    });

    it('ignores broken storage and keeps failures visible when a write is refused', () => {
        setFailedUploadHistoryAccount(42);
        rememberFailedUploadActivity(batch());
        const [key] = storage.keys();
        setFailedUploadHistoryAccount(0);
        for (const invalid of ['not json', '{"version":99,"entries":[]}', 'x'.repeat(1_500_000)]) {
            storage.set(key, invalid);
            setFailedUploadHistoryAccount(42);
            expect(get(failedUploadHistory)).toEqual([]);
            setFailedUploadHistoryAccount(0);
        }
        setFailedUploadHistoryAccount(42);
        setItem.mockImplementationOnce(() => { throw new Error('quota exceeded'); });
        expect(() => rememberFailedUploadActivity(batch())).not.toThrow();
        expect(get(failedUploadHistory)).toHaveLength(1);
    });
});
