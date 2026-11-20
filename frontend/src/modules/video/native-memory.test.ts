import { describe, expect, it } from 'vitest';
import { forgetNativePlayer, prefersNativePlayer, rememberNativePlayer, type NativeMemoryStorage } from './native-memory';

const clip = { accountId: 7, channelId: 1, id: 42, name: 'holiday.mp4', size: 1234 };

function memoryStorage(seed: Record<string, string> = {}): NativeMemoryStorage & { data: Map<string, string> } {
    const data = new Map(Object.entries(seed));
    return {
        data,
        getItem: (key) => data.get(key) ?? null,
        setItem: (key, value) => {
            data.set(key, value);
        },
    };
}

describe('native player memory', () => {
    it('does not know a file until the webview has failed on it', () => {
        const storage = memoryStorage();
        expect(prefersNativePlayer(clip, storage)).toBe(false);
        rememberNativePlayer(clip, storage);
        expect(prefersNativePlayer(clip, storage)).toBe(true);
    });

    it('treats a renamed or replaced upload as a different file', () => {
        const storage = memoryStorage();
        rememberNativePlayer(clip, storage);
        expect(prefersNativePlayer({ ...clip, name: 'holiday-cut.mp4' }, storage)).toBe(false);
        expect(prefersNativePlayer({ ...clip, size: 999 }, storage)).toBe(false);
    });

    it('scopes equal file IDs and names to a drive and account', () => {
        const storage = memoryStorage();
        const otherDrive = { ...clip, channelId: 2 };
        const otherAccount = { ...clip, accountId: 8 };
        rememberNativePlayer(clip, storage);

        expect(prefersNativePlayer(otherDrive, storage)).toBe(false);
        expect(prefersNativePlayer(otherAccount, storage)).toBe(false);

        rememberNativePlayer(otherDrive, storage);
        forgetNativePlayer(otherDrive, storage);

        expect(prefersNativePlayer(clip, storage)).toBe(true);
    });

    it('distinguishes typed Telegram peers that share a numeric ID', () => {
        const storage = memoryStorage();
        const group = { ...clip, sourceIdentity: 'source:7:group:1' };
        const channel = { ...clip, sourceIdentity: 'source:7:channel:1' };
        rememberNativePlayer(group, storage);

        expect(prefersNativePlayer(group, storage)).toBe(true);
        expect(prefersNativePlayer(channel, storage)).toBe(false);
        expect(prefersNativePlayer(clip, storage)).toBe(false);
    });

    it('ignores older hints that have no drive or account', () => {
        const storage = memoryStorage({ 'tdrive.video.nativeOnly': JSON.stringify(['42:holiday.mp4:1234']) });
        expect(prefersNativePlayer(clip, storage)).toBe(false);
    });

    it('clears a failed native preference without changing other videos', () => {
        const storage = memoryStorage();
        const other = { ...clip, id: 43 };
        rememberNativePlayer(clip, storage);
        rememberNativePlayer(other, storage);

        forgetNativePlayer(clip, storage);

        expect(prefersNativePlayer(clip, storage)).toBe(false);
        expect(prefersNativePlayer(other, storage)).toBe(true);
    });

    it('keeps the newest files when the list is full', () => {
        const storage = memoryStorage();
        for (let id = 1; id <= 205; id += 1) rememberNativePlayer({ ...clip, id, name: 'clip.mp4', size: id }, storage);
        expect(prefersNativePlayer({ ...clip, id: 1, name: 'clip.mp4', size: 1 }, storage)).toBe(false);
        expect(prefersNativePlayer({ ...clip, id: 205, name: 'clip.mp4', size: 205 }, storage)).toBe(true);
    });

    it('survives corrupt or unavailable storage', () => {
        const storage = memoryStorage({ 'tdrive.video.nativeOnly.v2': '{not json' });
        expect(prefersNativePlayer(clip, storage)).toBe(false);
        rememberNativePlayer(clip, storage);
        expect(prefersNativePlayer(clip, storage)).toBe(true);

        const broken: NativeMemoryStorage = {
            getItem: () => {
                throw new Error('storage disabled');
            },
            setItem: () => {
                throw new Error('storage full');
            },
        };
        expect(prefersNativePlayer(clip, broken)).toBe(false);
        expect(() => rememberNativePlayer(clip, broken)).not.toThrow();
        expect(prefersNativePlayer(clip, null)).toBe(false);
        expect(() => rememberNativePlayer(clip, null)).not.toThrow();
    });
});
