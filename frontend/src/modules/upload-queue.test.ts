import { afterEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';
import { clearUploadActivities, uploadActivities } from '../ui/notifications/upload-activity-store';
import { createUploadQueue, removeQueuedUpload } from './upload-queue';

afterEach(clearUploadActivities);
const metadata = (id: string, driveId = 1) => ({ id, name: id, driveId, driveTitle: 'Personal', destination: 'Photos', encrypt: false, fileCount: 1,
    files: [{ index: 0, name: `${id}.jpg`, status: 'queued' as const }] });

describe('interactive upload queue', () => {
    it('keeps FIFO order and releases waiting sources when removed', async () => {
        let resolveFirst = () => {};
        const first = { promise: new Promise<void>((resolve) => { resolveFirst = resolve; }), resolve: () => resolveFirst() };
        const run = vi.fn(async (id: string) => { if (id === 'one') await first.promise; });
        const queue = createUploadQueue({ canRun: () => true });
        const active = queue.add({ activity: metadata('one'), run: () => run('one') });
        const release = vi.fn();
        await queue.add({ activity: metadata('two'), run: () => run('two'), release });
        await queue.add({ activity: metadata('three'), run: () => run('three') });
        queue.remove('two');
        expect(release).toHaveBeenCalledOnce();
        expect(run.mock.calls).toEqual([['one']]);
        first.resolve();
        await active;
        await vi.waitFor(() => expect(run.mock.calls).toEqual([['one'], ['three']]));
        queue.dispose();
    });

    it('runs the open drive in order while another drive waits', async () => {
        let driveId = 2;
        const run = vi.fn(async (_id: string) => {});
        const queue = createUploadQueue({ canRun: (entry) => entry.driveId === driveId });
        await queue.add({ activity: metadata('one', 1), run: () => run('one') });
        await queue.add({ activity: metadata('two', 2), run: () => run('two') });
        await queue.add({ activity: metadata('three', 2), run: () => run('three') });
        expect(run.mock.calls).toEqual([['two'], ['three']]);
        expect(get(uploadActivities).find(({ id }) => id === 'one')).toMatchObject({ status: 'queued' });
        expect(get(uploadActivities).find(({ id }) => id === 'one')?.error).toBeUndefined();
        expect(queue.waitingFor(1)).toBe(true);
        expect(queue.waitingFor(2)).toBe(false);
        driveId = 1;
        await queue.wake();
        expect(run.mock.calls).toEqual([['two'], ['three'], ['one']]);
        queue.dispose();
    });

    it('drops queued closures on teardown and ignores late active completion', async () => {
        let resolveFirst = () => {};
        const first = { promise: new Promise<void>((resolve) => { resolveFirst = resolve; }), resolve: () => resolveFirst() };
        const waiting = vi.fn(async () => {});
        const release = vi.fn();
        const queue = createUploadQueue({ canRun: () => true });
        const active = queue.add({ activity: metadata('one'), run: () => first.promise });
        await queue.add({ activity: metadata('two'), run: waiting, release });
        queue.dispose();
        clearUploadActivities();
        first.resolve();
        await active;
        expect(waiting).not.toHaveBeenCalled();
        expect(release).toHaveBeenCalledOnce();
        expect(get(uploadActivities)).toEqual([]);
    });
    it('starts an eligible next batch when a blocked head is removed', async () => {
        const run = vi.fn(async () => {});
        const queue = createUploadQueue({ canRun: (entry) => entry.driveId === 2 });
        await queue.add({ activity: metadata('blocked', 1), run });
        await queue.add({ activity: metadata('ready', 2), run });
        queue.remove('blocked');
        await vi.waitFor(() => expect(run).toHaveBeenCalledOnce());
        queue.dispose();
    });

    it('bounds waiting batches and leaves accepted selections intact when full', async () => {
        const queue = createUploadQueue({ canRun: () => false });
        for (let index = 0; index < 10; index++) await queue.add({ activity: metadata(`queued-${index}`), run: async () => {} });
        expect(() => queue.add({ activity: metadata('overflow'), run: async () => {} })).toThrow('10 waiting batches');
        expect(get(uploadActivities)).toHaveLength(10);
        queue.dispose();
        expect(() => queue.add({ activity: metadata('late'), run: async () => {} })).toThrow('session has ended');
    });

    it('does not let an old disposal unlink removal for a replacement session', async () => {
        const old = createUploadQueue({ canRun: () => false });
        old.register();
        const current = createUploadQueue({ canRun: () => false });
        current.register();
        await current.add({ activity: metadata('current'), run: async () => {} });
        old.dispose();
        removeQueuedUpload('current');
        expect(get(uploadActivities)[0].status).toBe('canceled');
        current.dispose();
        removeQueuedUpload('missing');
    });

    it('records a runner failure and permits the next batch to run', async () => {
        const queue = createUploadQueue({ canRun: () => true });
        await queue.add({ activity: metadata('broken'), run: async () => { throw new Error('Source unavailable'); } });
        const run = vi.fn(async () => {});
        await queue.add({ activity: metadata('next'), run });
        expect(get(uploadActivities).find(({ id }) => id === 'broken')).toMatchObject({ status: 'failed', error: 'Source unavailable' });
        expect(run).toHaveBeenCalledOnce();
        queue.remove('already-finished');
        queue.dispose();
    });

});
