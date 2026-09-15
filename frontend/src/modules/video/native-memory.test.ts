import { describe, expect, it } from 'vitest';
import { prefersNativePlayer, rememberNativePlayer, type NativeMemoryStorage } from './native-memory';

const clip = { id: 42, name: 'holiday.mp4', size: 1234 };

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

    it('keeps the newest files when the list is full', () => {
        const storage = memoryStorage();
        for (let id = 1; id <= 205; id += 1) rememberNativePlayer({ id, name: 'clip.mp4', size: id }, storage);
        expect(prefersNativePlayer({ id: 1, name: 'clip.mp4', size: 1 }, storage)).toBe(false);
        expect(prefersNativePlayer({ id: 205, name: 'clip.mp4', size: 205 }, storage)).toBe(true);
    });

    it('survives corrupt or unavailable storage', () => {
        const storage = memoryStorage({ 'tdrive.video.nativeOnly': '{not json' });
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
