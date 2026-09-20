import { describe, expect, it } from 'vitest';
import { normalizeAsset, normalizePhotoBackupState } from './photo-backup';

describe('normalizeAsset', () => {
    it('carries the capture time and reads a host that omits it as unknown', () => {
        expect(normalizeAsset({ id: 'a', version: '1', media_type: 'photo', modified_at: 9, created_at: 4 })).toMatchObject({ modifiedAt: 9, createdAt: 4 });
        expect(normalizeAsset({ id: 'a', version: '1', media_type: 'photo', modified_at: 9 })?.createdAt).toBe(0);
        expect(normalizeAsset({ id: 'a', version: '1', media_type: 'document' })).toBeNull();
    });
});

describe('normalizePhotoBackupState', () => {
    it('keeps only a safe, actionable backup state from backend data', () => {
        const state = normalizePhotoBackupState({
            settings: { enabled: true, photos: true, videos: false, wifi_only: true },
            sources: [{ id: 'library:all', kind: 'library', name: 'All photos', enabled: true, added_at: 4 }, { id: '' }],
            status: { phase: 'uploading', pending: 12, uploading: 1, complete: 3, failed: -1, bytes_done: 50, bytes_total: 100 },
            capabilities: { wifi_only: { supported: true }, access: { status: 'limited', detail: 'Only selected photos are available.' } },
            platform: 'ios',
        });
        expect(state).toMatchObject({ settings: { enabled: true, videos: false, wifiOnly: true }, sources: [{ id: 'library:all', name: 'All photos' }], status: { phase: 'uploading', pending: 12, failed: 0, bytesDone: 50 }, platform: 'ios' });
        expect(state.capabilities).toEqual(expect.objectContaining({ wifiOnly: { supported: true, label: '', detail: '' } }));
    });

    it('normalizes only bounded current-file progress for live backup activity', () => {
        const state = normalizePhotoBackupState({
            status: { current_file: 'Trips/photo.jpg', current_file_bytes_done: 80, current_file_bytes_total: 100, current_file_percent: 80 },
        });
        expect(state.status).toMatchObject({ currentFile: 'Trips/photo.jpg', currentFileBytesDone: 80, currentFileBytesTotal: 100, currentFilePercent: 80 });
        expect(normalizePhotoBackupState({ status: { current_file: 'x'.repeat(600), current_file_percent: 900 } }).status)
            .toMatchObject({ currentFile: 'x'.repeat(512), currentFilePercent: 100 });
    });

    it('falls back safely for incomplete or contradictory state', () => {
        const state = normalizePhotoBackupState({ settings: {}, status: { phase: 'unknown', pending: -100 } });
        expect(state.settings).toEqual({ enabled: false, photos: true, videos: true, wifiOnly: false, encrypt: true });
        expect(state.status).toMatchObject({ phase: 'idle', pending: 0 });
        expect(state.sources).toEqual([]);
    });
});
