import { describe, expect, it } from 'vitest';
import type { TransferEvent } from './notif-store';
import {
    etaSeconds,
    transferItemDetail,
    formatAge,
    formatEta,
    formatSizePair,
    transferDetail,
    transferPercent,
    transferPhase,
    transferredBytes,
} from './transfer-view';

const NOW = 1_700_000_000_000;

function transfer(overrides: Partial<TransferEvent> = {}): TransferEvent {
    return {
        kind: 'transfer',
        id: 'xfer:up:1',
        direction: 'up',
        name: 'Clip.mp4',
        progress: 0,
        total: 0,
        bytes: 0,
        speed: 0,
        status: 'active',
        startedAt: NOW - 10_000,
        finishedAt: 0,
        ...overrides,
    };
}

describe('transferPhase', () => {
    it('keeps queued and paused apart: one will start itself, the other will not', () => {
        expect(transferPhase(transfer({ status: 'queued', total: 4_000 }))).toBe('waiting');
        expect(transferPhase(transfer({ status: 'paused', total: 4_000 }))).toBe('paused');
    });

    it('calls a started transfer with nothing measurable yet preparing', () => {
        expect(transferPhase(transfer({ status: 'active' }))).toBe('preparing');
    });

    it('leaves preparing as soon as any figure arrives', () => {
        expect(transferPhase(transfer({ status: 'active', total: 1_000 }))).toBe('running');
        expect(transferPhase(transfer({ status: 'active', itemsTotal: 3 }))).toBe('running');
        expect(transferPhase(transfer({ status: 'active', progress: 4 }))).toBe('running');
    });

    it('keeps every terminal state distinct', () => {
        expect(transferPhase(transfer({ status: 'done' }))).toBe('done');
        expect(transferPhase(transfer({ status: 'failed' }))).toBe('failed');
        expect(transferPhase(transfer({ status: 'canceled' }))).toBe('canceled');
        expect(transferPhase(transfer({ status: 'canceling' }))).toBe('canceling');
    });
});

describe('transferPercent', () => {
    it('is null while preparing, so the bar moves instead of claiming zero', () => {
        expect(transferPercent(transfer({ status: 'active' }))).toBeNull();
    });

    it('is zero, not null, for a queued transfer that has a place in line', () => {
        expect(transferPercent(transfer({ status: 'queued', total: 4_000 }))).toBe(0);
    });

    it('is full on success and absent once a transfer has stopped', () => {
        expect(transferPercent(transfer({ status: 'done', progress: 100 }))).toBe(100);
        expect(transferPercent(transfer({ status: 'failed', progress: 41 }))).toBeNull();
        expect(transferPercent(transfer({ status: 'canceled' }))).toBeNull();
    });

    it('clamps a backend that overshoots', () => {
        expect(transferPercent(transfer({ status: 'active', progress: 140, total: 10 }))).toBe(100);
        expect(transferPercent(transfer({ status: 'active', progress: -5, total: 10 }))).toBe(0);
    });
});

describe('transferredBytes', () => {
    it('believes progress over a byte count that has not caught up', () => {
        expect(transferredBytes(transfer({ progress: 50, total: 1_000, bytes: 0 }))).toBe(500);
    });

    it('never reports more than the whole', () => {
        expect(transferredBytes(transfer({ progress: 100, total: 1_000, bytes: 9_999 }))).toBe(1_000);
    });

    it('falls back to the raw count when no total is known', () => {
        expect(transferredBytes(transfer({ bytes: 120, total: 0 }))).toBe(120);
    });
});

describe('etaSeconds', () => {
    it('divides what is left by the rate', () => {
        expect(etaSeconds(transfer({ progress: 50, total: 1_000, speed: 100 }), NOW)).toBe(5);
    });

    it('falls back to the pace so far when there is no byte total to divide', () => {
        // Ten seconds to get halfway is ten seconds to finish. This is all an
        // aggregate has: a batch does not know its own size until its last file
        // has been read, and a number of files is not a number of bytes.
        expect(etaSeconds(transfer({ progress: 50, total: 0, speed: 0 }), NOW)).toBe(10);
        expect(etaSeconds(transfer({ progress: 50, total: 1_000, speed: 0 }), NOW)).toBe(10);
    });

    it('declines to guess before there is enough of the job to guess from', () => {
        expect(etaSeconds(transfer({ progress: 1, total: 0, speed: 0 }), NOW)).toBeNull();
        expect(etaSeconds(transfer({ progress: 100, total: 1_000, speed: 100 }), NOW)).toBeNull();
        expect(etaSeconds(transfer({ progress: 50, total: 0, speed: 0, startedAt: 0 }), NOW)).toBeNull();
    });

    it('says nothing rather than something absurd', () => {
        // A byte a second against a terabyte: true, useless, and alarming.
        expect(etaSeconds(transfer({ progress: 0, total: 1e12, speed: 1 }), NOW)).toBeNull();
    });

    it('is only for a transfer that is actually moving', () => {
        expect(etaSeconds(transfer({ status: 'queued', progress: 0, total: 1_000, speed: 100 }), NOW)).toBeNull();
    });
});

describe('formatEta', () => {
    it('rounds up, so a countdown never sits on a figure it has passed', () => {
        expect(formatEta(61)).toBe('2 min left');
        expect(formatEta(30)).toBe('30s left');
    });

    it('stops pretending to precision it does not have near the end', () => {
        expect(formatEta(2)).toBe('Almost done');
    });

    it('carries hours without dropping the minutes', () => {
        expect(formatEta(3600)).toBe('1 hr left');
        expect(formatEta(3600 + 20 * 60)).toBe('1 hr 20 min left');
    });
});

describe('formatSizePair', () => {
    it('names a shared unit once', () => {
        expect(formatSizePair(679 * 1024 * 1024, 942 * 1024 * 1024)).toBe('679 of 942 MB');
    });

    it('names both when they differ', () => {
        expect(formatSizePair(679 * 1024 * 1024, 2 * 1024 * 1024 * 1024)).toBe('679 MB of 2 GB');
    });
});

describe('formatAge', () => {
    it('is coarse, because a finished transfer is history rather than a clock', () => {
        expect(formatAge(NOW - 30_000, NOW)).toBe('just now');
        expect(formatAge(NOW - 5 * 60_000, NOW)).toBe('5 min ago');
        expect(formatAge(NOW - 3 * 3_600_000, NOW)).toBe('3 hr ago');
        expect(formatAge(NOW - 2 * 86_400_000, NOW)).toBe('2d ago');
    });

    it('says nothing for a transfer with no recorded end', () => {
        expect(formatAge(0, NOW)).toBe('');
    });
});

describe('transferDetail', () => {
    it('names the states that used to render as an empty bar', () => {
        expect(transferDetail(transfer({ status: 'queued', total: 3_000 }), NOW)).toBe('Waiting its turn');
        expect(transferDetail(transfer({ status: 'active' }), NOW)).toBe('Preparing…');
        expect(transferDetail(transfer({ status: 'canceling', progress: 22 }), NOW)).toBe('Stopping…');
    });

    it('does not send a paused transfer looking for a queue that is not there', () => {
        // Suspended with the app, not waiting behind other work.
        expect(transferDetail(transfer({ status: 'paused', progress: 40, total: 1_000 }), NOW)).toBe('Paused');
        // Stopped is the terminal cousin: put down rather than suspended, so it
        // says when, and Clear may take it.
        expect(transferDetail(transfer({ status: 'stopped', progress: 40, total: 1_000, finishedAt: NOW - 60_000 }), NOW)).toBe('Paused · 1 min ago');
        // The bar holds where it stopped: how far it got is still true, and it
        // is what the reader weighs before starting again.
        expect(transferPercent(transfer({ status: 'paused', progress: 40, total: 1_000 }))).toBe(40);
    });

    it('reads how far, then how long, then how fast', () => {
        const detail = transferDetail(transfer({
            progress: 50, total: 1024 * 1024 * 1000, speed: 1024 * 1024 * 5,
        }), NOW);
        expect(detail).toBe('500 of 1000 MB · 2 min left · 5 MB/s');
    });

    it('counts files, then says how much and how long, for a batch with no byte total', () => {
        expect(transferDetail(transfer({ itemsDone: 3, itemsTotal: 12, progress: 25, bytes: 5 * 1024 * 1024 }), NOW))
            .toBe('3 of 12 files · 5 MB so far · 30s left');
    });

    it('falls back to a bare percentage when nothing else is known', () => {
        expect(transferDetail(transfer({ progress: 42 }), NOW)).toBe('42% · 14s left');
    });

    it('dates a finished transfer, which the row never used to say', () => {
        expect(transferDetail(transfer({ status: 'done', finishedAt: NOW - 120_000 }), NOW))
            .toBe('Done · 2 min ago');
        expect(transferDetail(transfer({ status: 'failed', finishedAt: NOW - 120_000 }), NOW))
            .toBe('Failed · 2 min ago');
    });
});

describe('transferItemDetail', () => {
    it('gives a file its size pair, because the bar beside it already says the percentage', () => {
        expect(transferItemDetail({ key: '1', name: 'clip.mov', progress: 25, total: 4 * 1024 * 1024 }))
            .toBe('1 of 4 MB');
    });

    it('falls back to the percentage for a file whose size never arrived', () => {
        expect(transferItemDetail({ key: '1', name: 'clip.mov', progress: 25.4, total: 0 })).toBe('25%');
    });
});
