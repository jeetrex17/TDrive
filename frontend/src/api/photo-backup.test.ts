import { describe, expect, it } from 'vitest';
import { normalizePhotoBackupState } from './photo-backup';

describe('normalizePhotoBackupState', () => {
    it('keeps only a safe, actionable backup state from backend data', () => {
        const state = normalizePhotoBackupState({
            settings: { enabled: true, photos: true, videos: false, future_only: true, wifi_only: true },
            sources: [{ id: 'library:all', kind: 'library', name: 'All photos', enabled: true, added_at: 4 }, { id: '' }],
            status: { phase: 'uploading', pending: 12, uploading: 1, complete: 3, failed: -1, bytes_done: 50, bytes_total: 100 },
            capabilities: { wifi_only: { supported: true }, charging_only: { supported: false, label: 'Not reported by this device' }, access: { status: 'limited', detail: 'Only selected photos are available.' } },
            platform: 'ios',
        });
        expect(state).toMatchObject({ settings: { enabled: true, videos: false, futureOnly: true, wifiOnly: true }, sources: [{ id: 'library:all', name: 'All photos' }], status: { phase: 'uploading', pending: 12, failed: 0, bytesDone: 50 }, platform: 'ios' });
        expect(state.capabilities.chargingOnly).toEqual({ supported: false, label: 'Not reported by this device', detail: '' });
    });

    it('falls back safely for incomplete or contradictory state', () => {
        const state = normalizePhotoBackupState({ settings: {}, status: { phase: 'unknown', pending: -100 } });
        expect(state.settings).toEqual({ enabled: false, photos: true, videos: true, futureOnly: false, wifiOnly: false, chargingOnly: false });
        expect(state.status).toMatchObject({ phase: 'idle', pending: 0 });
        expect(state.sources).toEqual([]);
    });
});
