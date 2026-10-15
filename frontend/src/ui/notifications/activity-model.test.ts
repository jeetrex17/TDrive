import { describe, expect, it } from 'vitest';
import { activityEntries, activityTimestamp, batchLabel, batchActivityFilter, isBatchPaused, matchesActivity } from './activity-model';
import type { TransferEvent } from './notif-store';
import type { UploadActivity, UploadActivityStatus } from './upload-activity-store';
import type { ResumableUpload } from '../../api/resumable-uploads';
import type { ResumableDownload } from '../../api/resumable-downloads';

function batch(status: UploadActivityStatus = 'queued'): UploadActivity {
    return { id: 'batch-1', name: 'Summer photos', driveId: 3, driveTitle: 'Family', destination: 'Trips / Italy', encrypt: false, addedAt: 1, startedAt: 0, finishedAt: 0, status, fileCount: 2, files: [{ index: 0, name: 'Venice.jpg', status }, { index: 1, name: 'Rome.jpg', status }] };
}
function transfer(status: TransferEvent['status']): TransferEvent {
    return { kind: 'transfer', id: 'xfer:down:1', name: 'Video.mp4', direction: 'down', progress: 25, bytes: 250, total: 1000, speed: 0, status, startedAt: 0, finishedAt: 0 };
}
const upload: ResumableUpload = { jobId: 'job', channelId: 3, name: 'archive.bin', size: 100, confirmedBytes: 50, status: 'paused', error: '' };
const download: ResumableDownload = { jobId: 'download', channelId: 3, logicalMsgId: 7, name: 'image.jpg', totalBytes: 100, verifiedBytes: 20, speed: 0, savedPath: '', status: 'paused', error: '' };

describe('activity view model', () => {
    it.each([
        ['queued', 'Queued', 'Queued', 'Queued · 2 files'], ['active', 'Active', 'Uploading', 'Uploading · 2 files'],
        ['done', 'History', 'Completed', 'Completed · 2 files'], ['failed', 'Needs attention', 'Needs attention', 'Needs attention · 2 of 2 files failed'],
        ['canceled', 'History', 'Canceled', 'Canceled · 2 files'],
    ] as const)('keeps %s batches in the matching filter', (status, category, label, summary) => {
        const source = batch(status);
        const [entry] = activityEntries([source], [], [], []);
        expect(entry).toMatchObject({ id: 'batch-1', status: category, summary });
        expect(batchLabel(source)).toBe(label);
        expect(source.status).toBe(status);
    });

    it('links aggregate progress without replacing full file records', () => {
        const source = { ...batch('active'), fileCount: 1, transferId: 'xfer:down:1' };
        const aggregate = transfer('active');
        const entries = activityEntries([source], [aggregate], [], []);
        expect(entries).toHaveLength(1);
        expect(entries[0]).toMatchObject({ kind: 'batch', summary: 'Uploading · 25%', transfer: aggregate, batch: { files: [{ name: 'Venice.jpg' }, { name: 'Rome.jpg' }] } });
    });

    it('pauses a queued batch while another drive is open', () => {
        const source = batch();
        expect(isBatchPaused(source, 3)).toBe(false);
        expect(isBatchPaused(source, 4)).toBe(true);
        expect(isBatchPaused(source, undefined)).toBe(false);
        expect(isBatchPaused(source, null)).toBe(false);
        expect(isBatchPaused({ ...source, status: 'active' }, 4)).toBe(false);
        expect(batchLabel(source, [], 4)).toBe('Paused until Family is open');
        expect(activityEntries([source], [], [], [], { activeDriveId: 4 })[0].summary).toBe('Paused until Family is open · 2 files');
    });

    it('lists running work, then the queue in run order, then attention and newest history', () => {
        const now = 10_000_000;
        const entries = activityEntries([
            { ...batch('done'), id: 'old-done', finishedAt: now - 120_000 },
            { ...batch('queued'), id: 'second-queued', addedAt: 20 },
            { ...batch('failed'), id: 'failed', finishedAt: now - 3_600_000 },
            { ...batch('queued'), id: 'first-queued', addedAt: 10 },
            { ...batch('done'), id: 'new-done', finishedAt: now - 1_000 },
            { ...batch('active'), id: 'running' },
        ], [], [], [], { now });
        expect(entries.map((entry) => entry.id)).toEqual(['running', 'first-queued', 'second-queued', 'failed', 'new-done', 'old-done']);
        expect(entries.find((entry) => entry.id === 'old-done')?.summary).toBe('Completed · 2 files · 2 min ago');
    });

    it.each([
        ['queued', 'Queued'], ['active', 'Active'], ['paused', 'Active'], ['canceling', 'Active'],
        ['failed', 'Needs attention'], ['done', 'History'], ['canceled', 'History'], ['stopped', 'History'],
    ] as const)('keeps %s legacy transfers in the matching filter', (status, category) => {
        const [entry] = activityEntries([], [transfer(status), { kind: 'event', id: 'notice', level: 'info', title: 'Notice', body: '', ts: 1 }], [], []);
        expect(entry).toMatchObject({ kind: 'transfer', name: 'Video.mp4', status: category });
        expect(activityEntries([], [transfer(status)], [], [])).toHaveLength(1);
    });

    it('keeps nameless transfers readable in either direction', () => {
        expect(activityEntries([], [{ ...transfer('done'), name: '', direction: 'up' }], [], [])[0].name).toBe('Upload');
        expect(activityEntries([], [{ ...transfer('done'), name: '' }], [], [])[0].name).toBe('Download');
    });

    it.each(['uploading', 'canceling', 'paused', 'needs_source', 'uncertain_manifest', 'waiting_network'] as const)('preserves journal upload state %s and distinct identity', (status) => {
        const [entry] = activityEntries([], [], [{ ...upload, status }], []);
        expect(entry).toMatchObject({ id: 'upload:job', name: 'archive.bin', status: status === 'uploading' || status === 'canceling' ? 'Active' : 'Needs attention', summary: status.replace(/_/g, ' ') });
    });

    it.each(['downloading', 'verifying', 'saving', 'paused', 'completed', 'needs_destination', 'source_changed'] as const)('preserves journal download state %s and distinct identity', (status) => {
        const [entry] = activityEntries([], [], [], [{ ...download, status }]);
        expect(entry).toMatchObject({ id: 'download:download', status: ['downloading', 'verifying', 'saving'].includes(status) ? 'Active' : 'Needs attention' });
    });

    it('exposes linked journal controls only through their owning batch', () => {
        const entries = activityEntries([{ ...batch(), resumableJobIds: ['job'] }], [], [upload, { ...upload, jobId: 'other', name: '' }], [{ ...download, name: '' }]);
        expect(entries.map((entry) => entry.id)).toEqual(['batch-1', 'upload:other', 'download:download']);
        expect(entries.map((entry) => entry.name)).toEqual(['Summer photos', 'Large upload', 'Download']);
    });

    it.each(['uploading', 'canceling', 'paused', 'uncertain_manifest'] as const)('keeps linked recovery %s visible under its live parent state', (status) => {
        const source = { ...batch('failed'), resumableJobIds: ['job'] };
        const jobs = [{ ...upload, status }];
        const [entry] = activityEntries([source], [], jobs, []);
        expect(entry.status).toBe(status === 'uploading' || status === 'canceling' ? 'Active' : 'Needs attention');
        expect(batchLabel(source, jobs)).toBe(status === 'uploading' ? 'Resuming upload' : status === 'canceling' ? 'Discarding upload' : 'Upload recovery needs attention');
        expect(batchActivityFilter({ ...source, status: 'active' }, jobs)).toBe('Active');
    });

    it('searches names, statuses, destinations and file names beyond the visible page', () => {
        const [entry] = activityEntries([batch()], [], [], []);
        for (const query of ['', '  ', 'SUMMER', 'queued', 'family', 'ITALY', ' Rome.jpg ']) expect(matchesActivity(entry, query), query).toBe(true);
        expect(matchesActivity(entry, 'missing.pdf')).toBe(false);
        const [legacy] = activityEntries([], [transfer('active')], [], []);
        expect(matchesActivity(legacy, 'VIDEO')).toBe(true);
        expect(matchesActivity(legacy, 'Rome')).toBe(false);
    });

    it('states missing timeline data instead of inventing dates', () => {
        expect(activityTimestamp()).toBe('Not recorded');
        expect(activityTimestamp(0)).toBe('Not recorded');
        expect(activityTimestamp(-1)).toBe('Not recorded');
        expect(activityTimestamp(1_700_000_000_000)).toContain('2023');
        expect(activityTimestamp(1_700_000_000_000)).not.toMatch(/\d:\d\d:\d\d/);
    });
});
