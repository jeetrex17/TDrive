import { describe, expect, it, vi } from 'vitest';
import { PlaybackResume, playbackResumeKey, playbackProgress, type ResumeStorage } from './playback-resume';

function storage(): ResumeStorage {
    const data = new Map<string, string>();
    return { getItem: (key) => data.get(key) ?? null, setItem: vi.fn((key, value) => { data.set(key, value); }) };
}
const sample = (currentTime: number, paused = false) => ({ currentTime, duration: 3600, paused, loading: false });

describe('playback position memory', () => {
    it('restores after reopening without letting startup zero replace the saved class position', () => {
        const saved = storage();
        const first = new PlaybackResume('account:class', saved);
        first.observe(sample(1234));
        first.flush();
        const reopened = new PlaybackResume('account:class', saved);
        expect(reopened.observe({ ...sample(0), duration: 0 })).toBeNull();
        reopened.flush();
        expect(reopened.observe(sample(0))).toBe(1234);
        reopened.observe(sample(0));
        reopened.flush();
        expect(new PlaybackResume('account:class', saved).observe(sample(0))).toBe(1234);
        reopened.observe(sample(1234));
        reopened.observe(sample(1240, true));
        expect(new PlaybackResume('account:class', saved).observe(sample(0))).toBe(1240);
    });
    it('throttles progress but flushes pause and the latest background/close position', () => {
        const saved = storage();
        let now = 0;
        const player = new PlaybackResume('class', saved, () => now);
        player.observe(sample(5));
        now = 1000;
        player.observe(sample(6));
        expect(saved.setItem).toHaveBeenCalledTimes(1);
        player.flush();
        expect(new PlaybackResume('class', saved).observe(sample(0))).toBe(6);
        now = 7000;
        player.observe(sample(12));
        expect(saved.setItem).toHaveBeenCalledTimes(3);
    });
    it('clears only on natural end and does not resurrect an ended video on teardown', () => {
        const saved = storage();
        const player = new PlaybackResume('class', saved);
        player.observe(sample(3598));
        expect(new PlaybackResume('class', saved).observe(sample(0))).toBe(3598);
        player.complete();
        player.observe(sample(3600, true));
        player.flush();
        expect(new PlaybackResume('class', saved).observe(sample(0))).toBeNull();
        player.observe(sample(20));
        expect(new PlaybackResume('class', saved).observe(sample(0))).toBe(20);
    });
    it('separates accounts, drives, revisions and sources without storing filenames', () => {
        const info = { channelId: 1, fileId: 2, revision: 3 };
        const base = playbackResumeKey(7, info);
        expect(base).not.toBe(playbackResumeKey(8, info));
        expect(base).not.toBe(playbackResumeKey(7, { ...info, channelId: 4 }));
        expect(base).not.toBe(playbackResumeKey(7, { ...info, revision: 4 }));
        expect(base).not.toBe(playbackResumeKey(7, info, 'source:7:user:1'));
        expect(playbackResumeKey(0, info)).toBeNull();
    });
    it('keeps the bookmark when seeking fails, then records a later manual seek or playback', () => {
        const saved = storage();
        new PlaybackResume('class', saved).observe(sample(1234));
        const reopened = new PlaybackResume('class', saved);
        expect(reopened.observe(sample(0))).toBe(1234);
        reopened.restoreFailed();
        reopened.observe(sample(0));
        reopened.flush();
        expect(new PlaybackResume('class', saved).observe(sample(0))).toBe(1234);
        reopened.observe(sample(60));
        expect(new PlaybackResume('class', saved).observe(sample(0))).toBe(60);
    });
    it('tolerates malformed or disabled storage and invalid playback times', () => {
        const bad = { getItem: () => '{', setItem: () => { throw new Error('disabled'); } };
        const player = new PlaybackResume('class', bad);
        expect(player.observe(sample(NaN))).toBeNull();
        expect(() => { player.observe(sample(5)); player.flush(); player.complete(); }).not.toThrow();
    });
});


it('publishes a duration-based bar while preserving legacy bookmarks', () => {
    const data = new Map<string, string>();
    const saved = { getItem: (key: string) => data.get(key) ?? null, setItem: (key: string, value: string) => { data.set(key, value); } };
    vi.stubGlobal('localStorage', saved);
    data.set('tdrive.playback-position.v1', JSON.stringify([{ key: 'legacy', seconds: 900 }]));
    let progress: ReadonlyMap<string, number> = new Map();
    const unsubscribe = playbackProgress.subscribe((value) => { progress = value; });
    try {
        expect(progress.has('legacy')).toBe(false);
        const player = new PlaybackResume('legacy');
        expect(player.observe(sample(0))).toBe(900);
        player.observe(sample(900));
        player.flush();
        expect(progress.get('legacy')).toBe(0.25);
        expect(new PlaybackResume('legacy').observe(sample(0))).toBe(900);
        player.complete();
        expect(progress.has('legacy')).toBe(false);
    } finally {
        unsubscribe();
        vi.unstubAllGlobals();
    }
});
