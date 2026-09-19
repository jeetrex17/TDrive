import { beforeEach, describe, expect, it } from 'vitest';
import type { PhotoBackupState } from '../../api/photo-backup';
import { activeTransfers, historyEvents } from '../../ui/notifications/notif-store';
import { get } from 'svelte/store';
import { clearHistory } from '../notif-bell';
import { clearPhotoBackupActivity, syncPhotoBackupActivity } from './activity';

function state(overrides: Partial<PhotoBackupState['status']> = {}): PhotoBackupState {
    return {
        settings: { enabled: true, photos: true, videos: true, futureOnly: false, wifiOnly: false, encrypt: false },
        sources: [],
        status: {
            phase: 'uploading', pending: 4, uploading: 1, complete: 2, failed: 0, paused: 0,
            bytesDone: 400, bytesTotal: 1_000, currentFile: 'Summer/photo.jpg',
            currentFileBytesDone: 40, currentFileBytesTotal: 100, currentFilePercent: 40,
            message: '', ...overrides,
        },
        capabilities: { wifiOnly: { supported: true, label: '', detail: '' }, access: { status: '', detail: '' } },
        platform: 'darwin', destination: { id: '1', title: 'Personal', kind: 'personal' }, manualPaused: false, encryptionRequired: false,
    };
}

describe('photo backup activity', () => {
    beforeEach(() => { historyEvents.set([]); clearPhotoBackupActivity(); });

    it('keeps one backup row whose bar is the queue and whose listed file is the current one', () => {
        syncPhotoBackupActivity(state());
        syncPhotoBackupActivity(state({
            currentFile: 'Summer/photo-2.jpg', currentFileBytesDone: 75, currentFilePercent: 75,
            complete: 3, bytesDone: 600,
        }));

        const entries = get(activeTransfers);
        expect(entries).toHaveLength(1);
        expect(entries[0]).toMatchObject({
            id: 'xfer:up:photo-backup', name: 'Photo backup',
            // The queue's bytes, not the current file's: 600 of 1,000.
            progress: 60, bytes: 600, total: 1_000, itemsDone: 3, itemsTotal: 8,
        });
        expect(entries[0].items).toEqual([
            { key: 'Summer/photo-2.jpg', name: 'photo-2.jpg', progress: 75, total: 100 },
        ]);
    });

    it('lists one file however large the camera roll is, and drops it when the run ends', () => {
        for (let index = 0; index < 500; index++) {
            syncPhotoBackupActivity(state({
                currentFile: `Roll/photo-${index}.jpg`, complete: index, bytesDone: index,
            }));
        }
        const running = get(activeTransfers);
        expect(running).toHaveLength(1);
        expect(running[0].items).toHaveLength(1);

        syncPhotoBackupActivity(state({ phase: 'complete', pending: 0, uploading: 0, currentFile: '' }));
        expect(get(historyEvents)[0]).toMatchObject({ id: 'xfer:up:photo-backup', items: undefined });
    });

    // A paused backup is not on its way, and a row that claims to be cannot be
    // cleared: "Photo backup paused" sat in the panel with a progress bar and
    // no way to dismiss it, because Clear keeps whatever is still moving.
    it('puts a paused backup down, where Clear can take it', () => {
        syncPhotoBackupActivity(state());
        expect(get(activeTransfers)).toHaveLength(1);

        syncPhotoBackupActivity(state({ phase: 'paused', currentFile: '', currentFileBytesDone: 0, currentFileBytesTotal: 0, currentFilePercent: 0 }));
        expect(get(activeTransfers)).toEqual([]);
        expect(get(historyEvents)[0]).toMatchObject({ status: 'stopped', name: 'Photo backup paused' });

        clearHistory();
        expect(get(historyEvents).filter((entry) => entry.id === 'xfer:up:photo-backup')).toHaveLength(0);
    });

    it('finalizes a completion summary instead of retaining the last filename, and removes it on a scope change', () => {
        syncPhotoBackupActivity(state());
        syncPhotoBackupActivity(state({ phase: 'complete', pending: 0, uploading: 0, complete: 6, currentFile: '' }));
        expect(get(historyEvents)[0]).toMatchObject({ id: 'xfer:up:photo-backup', status: 'done', name: 'Photo backup completed · 6 items' });
        syncPhotoBackupActivity(state());
        clearPhotoBackupActivity();
        expect(get(activeTransfers)).toEqual([]);
        expect(get(historyEvents).filter((entry) => entry.id === 'xfer:up:photo-backup')).toHaveLength(0);
    });

    it('does not call an idle backup with failures or paused items complete', () => {
        syncPhotoBackupActivity(state());
        syncPhotoBackupActivity(state({ phase: 'idle', failed: 2, paused: 1, currentFile: '' }));
        expect(get(historyEvents)[0]).toMatchObject({ status: 'failed', name: 'Photo backup needs attention · 2 failed' });
    });

    it('does not report success when backup is turned off while an item is active', () => {
        syncPhotoBackupActivity(state());
        syncPhotoBackupActivity({ ...state({ phase: 'idle', currentFile: '' }), settings: { ...state().settings, enabled: false } });
        expect(get(historyEvents)[0]).toMatchObject({ status: 'canceled', name: 'Photo backup stopped' });
    });
});
