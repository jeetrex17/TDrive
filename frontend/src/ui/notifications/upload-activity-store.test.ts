import { afterEach, describe, expect, it } from 'vitest';
import { get } from 'svelte/store';
import { addUploadActivity, clearUploadActivityHistory, clearUploadActivities, finishUploadActivity, mergeUploadActivityFiles, startUploadActivity, updateUploadActivityFile, uploadActivities, uploadActivityFilesPage } from './upload-activity-store';

afterEach(clearUploadActivities);

function activity(id: string, count = 2) {
    return { id, name: 'Files', driveId: 1, driveTitle: 'Personal', destination: 'Personal / Photos', encrypt: false, fileCount: count,
        files: Array.from({ length: count }, (_, index) => ({ index, name: `${index}.jpg`, status: 'queued' as const })) };
}

describe('session upload details', () => {
    it('keeps duplicate names independent and reconciles final failures by request index', () => {
        addUploadActivity(activity('one'));
        startUploadActivity('one');
        updateUploadActivityFile('one', 0, { name: 'same.jpg', status: 'done', progress: 100 });
        updateUploadActivityFile('one', 1, { name: 'same.jpg', status: 'active', progress: 70 });
        finishUploadActivity('one', 'failed', [{ index: 1, message: 'Needs indexing' }]);
        expect(get(uploadActivities)[0].files).toEqual([
            expect.objectContaining({ index: 0, name: 'same.jpg', status: 'done' }),
            expect.objectContaining({ index: 1, name: 'same.jpg', status: 'failed', message: 'Needs indexing' }),
        ]);
        expect(uploadActivityFilesPage('one', 1, 1)).toEqual([expect.objectContaining({ index: 1 })]);
    });

    it('adds files a running import reaches and updates them in place', () => {
        addUploadActivity({ ...activity('import', 0), fileCount: 3 });
        startUploadActivity('import');
        mergeUploadActivityFiles('import', [{ index: 0, name: 'a.jpg', status: 'active', progress: 40 }]);
        mergeUploadActivityFiles('import', [{ index: 0, name: 'a.jpg', status: 'done', progress: 100, size: 9 }, { index: 1, name: 'b.jpg', status: 'failed', message: 'Lost' }]);
        mergeUploadActivityFiles('missing', [{ index: 0, name: 'x', status: 'done' }]);
        expect(get(uploadActivities)[0].files).toEqual([
            { index: 0, name: 'a.jpg', status: 'done', progress: 100, size: 9 },
            { index: 1, name: 'b.jpg', status: 'failed', message: 'Lost' },
        ]);
    });

    it('lets older finished batches give up their detail to a growing import', () => {
        addUploadActivity(activity('old', 6000));
        finishUploadActivity('old', 'done');
        addUploadActivity({ ...activity('import', 0), fileCount: 6000 });
        startUploadActivity('import');
        mergeUploadActivityFiles('import', Array.from({ length: 6000 }, (_, index) => ({ index, name: `${index}.jpg`, status: 'done' as const })));
        expect(get(uploadActivities).map(({ id }) => id)).toEqual(['import']);
        expect(get(uploadActivities)[0].files).toHaveLength(6000);
    });

    it('bounds retained file records and rejects a batch too large to retain safely', () => {
        addUploadActivity(activity('old', 6000));
        finishUploadActivity('old', 'done');
        addUploadActivity(activity('new', 6000));
        expect(get(uploadActivities).map(({ id }) => id)).toEqual(['new']);
        expect(() => addUploadActivity(activity('huge', 10001))).toThrow('10,000');
    });

    it('does not replace an unfinished record to make room for history', () => {
        addUploadActivity(activity('active', 6000));
        startUploadActivity('active');
        expect(() => addUploadActivity(activity('waiting', 6000))).toThrow('10,000');
        expect(get(uploadActivities)[0].id).toBe('active');
    });
    it('preserves completed files and canceled outcomes when the rest of a batch is stopped', () => {
        addUploadActivity(activity('cancel', 3));
        updateUploadActivityFile('cancel', 0, { status: 'done' });
        finishUploadActivity('cancel', 'canceled', [{ index: 1, code: 'canceled', message: 'Stopped' }]);
        expect(get(uploadActivities)[0].files.map(({ status }) => status)).toEqual(['done', 'canceled', 'canceled']);
    });

    it('clears only completed display records and bounds file pages', () => {
        addUploadActivity(activity('done', 3));
        finishUploadActivity('done', 'done');
        addUploadActivity(activity('waiting', 3));
        addUploadActivity(activity('active', 3));
        startUploadActivity('active');
        clearUploadActivityHistory();
        expect(get(uploadActivities).map(({ id }) => id)).toEqual(['active', 'waiting']);
        expect(uploadActivityFilesPage('missing')).toEqual([]);
        expect(uploadActivityFilesPage('active', -2, -1)).toEqual([]);
        expect(uploadActivityFilesPage('active', 1)).toHaveLength(2);
    });

    it('bounds batch history independently of the total retained files', () => {
        for (let index = 0; index < 101; index++) {
            addUploadActivity(activity(String(index), 0));
            finishUploadActivity(String(index), 'done');
        }
        expect(get(uploadActivities)).toHaveLength(100);
        expect(get(uploadActivities).some(({ id }) => id === '0')).toBe(false);
    });

});
