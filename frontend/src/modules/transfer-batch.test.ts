import { describe, expect, it } from 'vitest';
import { MAX_TRANSFER_ITEMS } from '../ui/notifications/notif-store';
import { TransferBatch } from './transfer-batch';

describe('TransferBatch', () => {
    it('counts a part-uploaded file as the part of it that has gone', () => {
        const batch = new TransferBatch(4);
        batch.start(0, 'a.bin', 1_000);
        batch.settle(0, 'done');
        batch.start(1, 'b.bin', 1_000);
        batch.progress(1, 50);

        // One file of four, and half of a second: 37.5%. Counting settled files
        // alone would say 25% and sit there for the whole of the second file.
        expect(batch.snapshot()).toMatchObject({
            progress: 37.5, itemsDone: 1, itemsTotal: 4, bytes: 1_500, itemsActive: 1,
        });
    });

    it('ignores a percentage that goes backwards, so a bar cannot un-fill', () => {
        const batch = new TransferBatch(1);
        batch.start(0, 'a.bin', 100);
        batch.progress(0, 80);
        batch.progress(0, 10);
        expect(batch.snapshot().progress).toBe(80);
    });

    it('keeps failed and canceled files apart while letting both settle the bar', () => {
        const batch = new TransferBatch(3);
        batch.start(0, 'a', 10);
        batch.start(1, 'b', 10);
        batch.start(2, 'c', 10);
        batch.settle(0, 'done');
        batch.settle(1, 'failed');
        batch.settle(2, 'canceled');

        const snapshot = batch.snapshot();
        expect(snapshot.progress).toBe(100);
        // Only the delivered file's bytes are on Telegram, so only those count.
        expect(snapshot.bytes).toBe(10);
        expect([batch.succeeded, batch.failures, batch.stopped]).toEqual([1, 1, 1]);
    });

    it('closes out files whose outcome never arrived, so the row cannot hang', () => {
        const batch = new TransferBatch(3);
        batch.start(0, 'a', 10);
        batch.settleRemaining('done');

        expect(batch.settled).toBe(3);
        expect(batch.snapshot()).toMatchObject({ progress: 100, itemsDone: 3, itemsActive: 0 });
    });

    it('retains only the files in flight, however many the batch holds', () => {
        const batch = new TransferBatch(10_000);
        for (let id = 0; id < 10_000; id++) {
            batch.start(id, `photo-${id}.jpg`, 1_000);
            batch.progress(id, 100);
            batch.settle(id, 'done');
        }

        const snapshot = batch.snapshot();
        expect(snapshot.items).toEqual([]);
        expect(snapshot.itemsActive).toBe(0);
        expect(snapshot).toMatchObject({ progress: 100, itemsDone: 10_000, bytes: 10_000_000 });
    });

    it('lists the oldest files in flight and says how many it left out', () => {
        const batch = new TransferBatch(100);
        for (let id = 0; id < 8; id++) batch.start(id, `clip-${id}.mov`, 100);

        const snapshot = batch.snapshot();
        expect(snapshot.items).toHaveLength(MAX_TRANSFER_ITEMS);
        expect(snapshot.items.map((item) => item.key)).toEqual(['0', '1', '2', '3']);
        expect(snapshot.itemsActive).toBe(8);
    });

    it('holds a file\'s place in the list while the ones around it finish', () => {
        const batch = new TransferBatch(10);
        batch.start(0, 'first.mov', 100);
        batch.start(1, 'second.mov', 100);
        batch.settle(0, 'done');
        batch.start(2, 'third.mov', 100);

        expect(batch.snapshot().items.map((item) => item.name)).toEqual(['second.mov', 'third.mov']);
    });
});
