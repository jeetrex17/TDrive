import { describe, expect, it } from 'vitest';
import type { TransferEvent } from './notif-store';
import {
    etaSeconds,
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
    it('treats queued and paused alike: not moving, nothing wrong', () => {
        expect(transferPhase(transfer({ status: 'queued', total: 4_000 }))).toBe('waiting');
        expect(transferPhase(transfer({ status: 'paused', total: 4_000 }))).toBe('waiting');
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
        expect(etaSeconds(transfer({ progress: 50, total: 1_000, speed: 100 }))).toBe(5);
    });

    it('declines to guess without a total, a rate, or anything left to send', () => {
        expect(etaSeconds(transfer({ progress: 50, total: 0, speed: 100 }))).toBeNull();
        expect(etaSeconds(transfer({ progress: 50, total: 1_000, speed: 0 }))).toBeNull();
        expect(etaSeconds(transfer({ progress: 100, total: 1_000, speed: 100 }))).toBeNull();
    });

    it('says nothing rather than something absurd', () => {
        // A byte a second against a terabyte: true, useless, and alarming.
        expect(etaSeconds(transfer({ progress: 0, total: 1e12, speed: 1 }))).toBeNull();
    });

    it('is only for a transfer that is actually moving', () => {
        expect(etaSeconds(transfer({ status: 'queued', progress: 0, total: 1_000, speed: 100 }))).toBeNull();
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

    it('reads how far, then how long, then how fast', () => {
        const detail = transferDetail(transfer({
            progress: 50, total: 1024 * 1024 * 1000, speed: 1024 * 1024 * 5,
        }), NOW);
        expect(detail).toBe('500 of 1000 MB · 2 min left · 5 MB/s');
    });

    it('counts files when that is what the backend knows', () => {
        expect(transferDetail(transfer({ itemsDone: 3, itemsTotal: 12, progress: 25 }), NOW))
            .toBe('3 of 12 files');
    });

    it('falls back to a bare percentage when nothing else is known', () => {
        expect(transferDetail(transfer({ progress: 42 }), NOW)).toBe('42%');
    });

    it('dates a finished transfer, which the row never used to say', () => {
        expect(transferDetail(transfer({ status: 'done', finishedAt: NOW - 120_000 }), NOW))
            .toBe('Done · 2 min ago');
        expect(transferDetail(transfer({ status: 'failed', finishedAt: NOW - 120_000 }), NOW))
            .toBe('Failed · 2 min ago');
    });
});
