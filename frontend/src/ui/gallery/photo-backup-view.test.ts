import { describe, expect, it } from 'vitest';
import type { PhotoBackupState } from '../../api/photo-backup';
import { actionLabel, canStart, currentFileName, describeBackup, destinationLabel, summaryLine } from './photo-backup-view';

function state(overrides: Partial<PhotoBackupState> = {}, status: Partial<PhotoBackupState['status']> = {}): PhotoBackupState {
    return {
        settings: { enabled: true, photos: true, videos: true, futureOnly: false, wifiOnly: false, encrypt: false },
        sources: [{ id: 'camera', kind: 'library', root: 'Camera', name: 'Camera', enabled: true, addedAt: 1 }],
        status: { phase: 'idle', pending: 0, uploading: 0, complete: 0, failed: 0, paused: 0, bytesDone: 0, bytesTotal: 0, currentFile: '', currentFileBytesDone: 0, currentFileBytesTotal: 0, currentFilePercent: 0, message: '', ...status },
        capabilities: { wifiOnly: { supported: false, label: '', detail: '' }, access: { status: 'available', detail: '' } },
        platform: 'darwin', destination: { id: '', title: 'Photo backup', kind: 'folder' },
        manualPaused: false, encryptionRequired: false,
        ...overrides,
    };
}

describe('describeBackup', () => {
    it('ranks the flags above the phase: locked beats paused beats uploading', () => {
        const busy = { phase: 'uploading' as const, uploading: 1, pending: 3 };
        expect(describeBackup(state({}, busy))).toMatchObject({ tone: 'busy', secondary: 'pause', progress: true });
        expect(describeBackup(state({ manualPaused: true }, busy))).toMatchObject({ tone: 'idle', title: 'Paused', primary: 'resume' });
        expect(describeBackup(state({ manualPaused: true, encryptionRequired: true }, busy))).toMatchObject({ tone: 'locked', primary: 'resume' });
        expect(describeBackup(state({ settings: { ...state().settings, enabled: false }, encryptionRequired: true }, busy))).toMatchObject({ title: 'Backup is off', primary: null });
    });

    it('sends an unlock into whatever the queue needs next', () => {
        expect(describeBackup(state({ encryptionRequired: true })).primary).toBe('start');
        expect(describeBackup(state({ encryptionRequired: true }, { failed: 2 })).primary).toBe('retry');
        expect(describeBackup(state({ encryptionRequired: true }, { paused: 1 })).primary).toBe('retry');
    });

    it('tells a device wait apart from interrupted items, which both arrive as paused', () => {
        expect(describeBackup(state({}, { phase: 'paused', message: 'Waiting for Wi-Fi.' }))).toMatchObject({ tone: 'warning', title: 'Waiting', body: 'Waiting for Wi-Fi.', primary: 'start' });
        expect(describeBackup(state({}, { phase: 'paused', paused: 2 }))).toMatchObject({ tone: 'warning', title: '2 items were interrupted', primary: 'retry' });
    });

    it('never repeats in the body what the summary line already counts', () => {
        expect(describeBackup(state({ manualPaused: true }, { pending: 12 })).body).not.toMatch(/\d/);
        expect(describeBackup(state({}, { phase: 'queued', pending: 12 })).body).toBe('');
        expect(describeBackup(state({}, { phase: 'complete', complete: 240 })).body).toBe('');
    });

    it('guides an empty setup and reports a finished one', () => {
        expect(describeBackup(state({ sources: [] }))).toMatchObject({ title: 'Choose what to back up', primary: null });
        expect(describeBackup(state({}, { phase: 'complete', complete: 1 }))).toMatchObject({ tone: 'success', title: 'Up to date', primary: 'start' });
        expect(describeBackup(state({}, { phase: 'failed', failed: 1, message: 'Telegram is unavailable.' }))).toMatchObject({ tone: 'danger', title: '1 item could not be backed up', detail: 'Telegram is unavailable.', primary: 'retry' });
    });

    it('names the file alone while uploading', () => {
        expect(describeBackup(state({}, { phase: 'uploading', currentFile: 'Trips/Summer/IMG_0042.HEIC' })).body).toBe('IMG_0042.HEIC');
        expect(currentFileName({ ...state().status, currentFile: 'C:\\Photos\\a.jpg' })).toBe('a.jpg');
    });
});

describe('summaryLine and labels', () => {
    it('leaves out zero counts', () => {
        expect(summaryLine({ ...state().status, complete: 24, pending: 2, uploading: 1 })).toBe('24 backed up · 3 waiting');
        expect(summaryLine({ ...state().status, failed: 1, paused: 1 })).toBe('1 failed · 1 interrupted');
        expect(summaryLine(state().status)).toBe('');
    });

    it('says what a locked action will do first', () => {
        expect(actionLabel('start', true)).toBe('Unlock and back up');
        expect(actionLabel('retry', false)).toBe('Retry');
        expect(actionLabel('pause', true)).toBe('Pause');
    });

    it('cannot start with nothing selected to back up', () => {
        expect(canStart(state())).toBe(true);
        expect(canStart(state({ sources: [] }))).toBe(false);
        expect(canStart(state({ settings: { ...state().settings, photos: false, videos: false } }))).toBe(false);
    });
});

describe('a failure reports a cause without becoming one', () => {
    // What the backend hands over is a path and an errno. It went straight into
    // the body once and filled half the panel with it.
    const raw = '/data/user/0/com.jeetraj.tdrive/files/TDrive/photo-backup-stage/'
        + 'image-1000219416/.england-london-bridge (2).jpg.partial: open failed: '
        + 'ENOENT (No such file or directory)';

    it('keeps the backend string out of the body and in the detail', () => {
        const situation = describeBackup(state({}, { phase: 'failed', failed: 2, message: raw }));
        expect(situation.detail).toBe(raw);
        expect(situation.body).not.toContain('ENOENT');
        expect(situation.body).not.toContain('/data/');
        // The body still has to say something useful on its own.
        expect(situation.body.length).toBeGreaterThan(0);
        expect(situation.title).toBe('2 items could not be backed up');
    });

    it('says nothing in the detail when there is no cause to report', () => {
        expect(describeBackup(state({}, { phase: 'failed', failed: 1 })).detail).toBe('');
        expect(describeBackup(state({}, { phase: 'complete' })).detail).toBe('');
    });
});

describe('destinationLabel', () => {
    it('drops the device suffix that only the folder needs', () => {
        expect(destinationLabel('Photo backup / Android device (3c48be52) / Download'))
            .toBe('Photo backup / Android device / Download');
    });

    it('leaves a name that merely ends in brackets alone', () => {
        // Only a hex run is the generated suffix; a real folder keeps its name.
        expect(destinationLabel('Photo backup / Mac / Holiday (2024)'))
            .toBe('Photo backup / Mac / Holiday (2024)');
        expect(destinationLabel('Photo backup / Mac / Scans (draft)'))
            .toBe('Photo backup / Mac / Scans (draft)');
    });

    it('passes through an empty or plain destination unchanged', () => {
        expect(destinationLabel('')).toBe('');
        expect(destinationLabel('Photo backup')).toBe('Photo backup');
    });
});
