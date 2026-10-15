import { afterEach, describe, expect, it } from 'vitest';
import { get } from 'svelte/store';
import { resumableUploads } from '../../modules/resumable-upload-store';
import { resumableDownloads } from '../../modules/resumable-download-store';
import { historyEvents, type TransferEvent } from './notif-store';
import { uploadActivities } from './upload-activity-store';
import { standaloneResumableUploads, visibleActiveTransfers, visibleHistory, visibleRecentEvents } from './visible-history';
const transfer: TransferEvent = { kind: 'transfer', id: 'xfer:up:1', name: 'file.bin', direction: 'up', progress: 20, bytes: 20, total: 100, speed: 0, status: 'active', startedAt: 1, finishedAt: 0 };
afterEach(() => { historyEvents.set([]); uploadActivities.set([]); resumableUploads.set([]); resumableDownloads.set([]); });

describe('shared activity history', () => {
    it('hides journal counterparts and keeps batch progress rows', () => {
        historyEvents.set([
            { ...transfer, resumableJobId: 'journal' },
            { ...transfer, id: 'xfer:down:file:3:7', direction: 'down' },
            { ...transfer, id: 'batch-transfer' },
            { ...transfer, id: 'ordinary-active' },
            { ...transfer, id: 'ordinary-done', status: 'done' },
            { kind: 'event', id: 'notice', level: 'error', title: 'Problem', body: 'Details', ts: 1 },
        ]);
        resumableUploads.set([{ jobId: 'journal', channelId: 3, name: 'file.bin', status: 'paused', size: 100, confirmedBytes: 20, error: '' }]);
        resumableDownloads.set([{ jobId: 'download', channelId: 3, logicalMsgId: 7, name: 'file.bin', status: 'paused', totalBytes: 100, verifiedBytes: 20, speed: 0, savedPath: '', error: '' }]);
        uploadActivities.set([{ id: 'batch', name: 'Selection', driveId: 3, driveTitle: 'Personal', destination: '/', encrypt: false, addedAt: 1, startedAt: 1, finishedAt: 0, status: 'active', files: [], fileCount: 5, transferId: 'batch-transfer', resumableJobIds: ['journal'] }]);
        expect(get(visibleHistory).map((entry) => entry.id)).toEqual(['batch-transfer', 'ordinary-active', 'ordinary-done', 'notice']);
        expect(get(visibleActiveTransfers).map((entry) => entry.id)).toEqual(['batch-transfer', 'ordinary-active']);
        expect(get(visibleRecentEvents).map((entry) => entry.id)).toEqual(['ordinary-done', 'notice']);
        expect(get(standaloneResumableUploads)).toEqual([]);
        uploadActivities.set([]);
        expect(get(standaloneResumableUploads).map((job) => job.jobId)).toEqual(['journal']);
    });
});
