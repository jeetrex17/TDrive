import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { MediaStats } from '../../api';
import {
    StreamActivityMonitor,
    formatStreamMultiplier,
    formatStreamRate,
    streamActivityLabel,
    type StreamActivityContext,
} from './stream-activity';

function stats(bytesPerSecond: number, recentFloodWait = false): MediaStats {
    const throughput = { bytesPerSecond, recentFloodWait, lastFloodWaitSeconds: 0 };
    return { playback: throughput, thumbnails: { ...throughput, bytesPerSecond: 0, recentFloodWait: false } };
}

describe('stream throughput wording', () => {
    it('reads small rates in KB/s and large ones in MB/s', () => {
        expect(formatStreamRate(64 * 1024)).toBe('64.0 KB/s');
        expect(formatStreamRate(3.5 * 1024 * 1024)).toBe('3.5 MB/s');
    });

    it('never rounds a live stream down to nothing, which would read as stalled', () => {
        expect(formatStreamRate(1)).toBe('0.1 KB/s');
    });

    it('treats a missing or nonsense rate as zero rather than printing NaN', () => {
        expect(formatStreamRate(Number.NaN)).toBe('0.1 KB/s');
        expect(formatStreamRate(-5)).toBe('0.1 KB/s');
    });

    it('says nothing about pace until it knows both the size and the duration', () => {
        expect(formatStreamMultiplier(1024, 0, 120)).toBe('');
        expect(formatStreamMultiplier(1024, 1024 * 1024, 0)).toBe('');
        expect(formatStreamMultiplier(0, 1024 * 1024, 120)).toBe('');
    });

    it('reports how far ahead of real time the download is running', () => {
        // 120 MB over 120s averages 1 MB/s, so 2 MB/s is twice real time.
        expect(formatStreamMultiplier(2 * 1024 * 1024, 120 * 1024 * 1024, 120)).toBe('(~2.0x)');
        expect(formatStreamMultiplier(25 * 1024 * 1024, 120 * 1024 * 1024, 120)).toBe('(~25x)');
    });

    it('caps an absurd burst instead of putting a four-digit number in the title bar', () => {
        expect(formatStreamMultiplier(500 * 1024 * 1024, 120 * 1024 * 1024, 120)).toBe('(~99x+)');
    });

    it('says a stall is Telegram rate-limiting us, not a slow connection', () => {
        expect(streamActivityLabel(stats(0, true), 0, 0)).toBe('Rate-limited');
    });

    it('says nothing at all when no bytes are moving and nothing is wrong', () => {
        expect(streamActivityLabel(stats(0), 0, 0)).toBe('');
    });

    it('combines the rate and the pace into one phrase', () => {
        expect(streamActivityLabel(stats(2 * 1024 * 1024), 120 * 1024 * 1024, 120))
            .toBe('Streaming 2.0 MB/s (~2.0x)');
    });
});

function makeReadStats() {
    return vi.fn((_token: string): Promise<MediaStats> => Promise.resolve(stats(2 * 1024 * 1024)));
}

describe('the stream activity monitor', () => {
    let token: string;
    let error: boolean;
    let changed: number;
    let readStats: ReturnType<typeof makeReadStats>;
    let monitor: StreamActivityMonitor;

    beforeEach(() => {
        vi.useFakeTimers();
        token = 'session-1';
        error = false;
        changed = 0;
        readStats = makeReadStats();
        const ctx: StreamActivityContext = {
            readStats: (value) => readStats(value),
            activeToken: () => token,
            hasError: () => error,
            mediaBytes: () => 120 * 1024 * 1024,
            durationSeconds: () => 120,
            changed: () => { changed += 1; },
        };
        monitor = new StreamActivityMonitor(ctx);
    });

    afterEach(() => {
        monitor.stop();
        vi.useRealTimers();
    });

    it('samples immediately on start, so the opening buffer is not a second of silence', async () => {
        monitor.sync();
        await vi.advanceTimersByTimeAsync(0);
        expect(readStats).toHaveBeenCalledTimes(1);
        expect(monitor.label).toBe('Streaming 2.0 MB/s (~2.0x)');
    });

    it('keeps polling once a second while a session is open', async () => {
        monitor.sync();
        await vi.advanceTimersByTimeAsync(2000);
        expect(readStats).toHaveBeenCalledTimes(3);
    });

    it('does not start a second poll loop when told to sync again', async () => {
        monitor.sync();
        monitor.sync();
        await vi.advanceTimersByTimeAsync(1000);
        expect(readStats).toHaveBeenCalledTimes(2);
    });

    it('stops polling when the player has no session', async () => {
        monitor.sync();
        await vi.advanceTimersByTimeAsync(0);
        token = '';
        monitor.sync();
        readStats.mockClear();
        await vi.advanceTimersByTimeAsync(3000);
        expect(readStats).not.toHaveBeenCalled();
        expect(monitor.label).toBe('');
    });

    it('stops polling while an error is on screen, where throughput is beside the point', async () => {
        error = true;
        monitor.sync();
        await vi.advanceTimersByTimeAsync(3000);
        expect(readStats).not.toHaveBeenCalled();
    });

    it('holds the last phrase through a quiet sample, so the line does not blink between read bursts', async () => {
        monitor.sync();
        await vi.advanceTimersByTimeAsync(0);
        readStats.mockImplementation(async () => stats(0));
        await vi.advanceTimersByTimeAsync(1000);
        expect(monitor.label).toBe('Streaming 2.0 MB/s (~2.0x)');
    });

    it('clears the phrase once the stream has been quiet longer than the hold', async () => {
        monitor.sync();
        await vi.advanceTimersByTimeAsync(0);
        readStats.mockImplementation(async () => stats(0));
        await vi.advanceTimersByTimeAsync(2500);
        expect(monitor.label).toBe('');
    });

    it('clears the phrase on its own timer when the stream stops producing samples entirely', async () => {
        monitor.sync();
        await vi.advanceTimersByTimeAsync(0);
        readStats.mockImplementation(() => new Promise<MediaStats>(() => {}));
        await vi.advanceTimersByTimeAsync(2500);
        expect(monitor.label).toBe('');
    });

    it('drops a sample that belongs to a session which has since been replaced', async () => {
        let release: ((value: MediaStats) => void) | null = null;
        readStats.mockImplementation(() => new Promise<MediaStats>((resolve) => { release = resolve; }));
        monitor.sync();
        await vi.advanceTimersByTimeAsync(0);
        token = 'session-2';
        release!(stats(9 * 1024 * 1024));
        await vi.advanceTimersByTimeAsync(0);
        expect(monitor.label).toBe('');
    });

    it('survives a failed stats call without stopping playback or the poll loop', async () => {
        const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
        readStats.mockRejectedValue(new Error('session not found'));
        monitor.sync();
        await vi.advanceTimersByTimeAsync(1000);
        expect(readStats).toHaveBeenCalledTimes(2);
        expect(monitor.label).toBe('');
        warn.mockRestore();
    });

    it('tells its surfaces to re-render when it stops, so the line clears with it', async () => {
        monitor.sync();
        await vi.advanceTimersByTimeAsync(0);
        const before = changed;
        monitor.stop();
        expect(changed).toBe(before + 1);
    });
});
