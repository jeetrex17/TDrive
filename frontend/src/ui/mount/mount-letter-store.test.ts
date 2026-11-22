import { describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';
import { createMountLetterStore } from './mount-letter-store';

describe('mount letter store', () => {
    it('stays hidden until the backend reports drive letters', async () => {
        const store = createMountLetterStore({
            load: vi.fn(async () => ({ available: false, letter: 'T:' })),
            save: vi.fn(),
        });
        expect(get(store).available).toBe(false);
        await store.load();
        expect(get(store)).toMatchObject({ available: false, letter: 'T:' });
    });

    it('loads once and retries after a failed load', async () => {
        const load = vi.fn()
            .mockRejectedValueOnce(new Error('bridge not ready'))
            .mockResolvedValue({ available: true, letter: 'M:' });
        const store = createMountLetterStore({ load, save: vi.fn() });

        await store.load();
        expect(get(store).available).toBe(false);
        await store.load();
        await store.load();
        expect(load).toHaveBeenCalledTimes(2);
        expect(get(store)).toMatchObject({ available: true, letter: 'M:' });
    });

    it('keeps the previous letter and reports why a save failed', async () => {
        const store = createMountLetterStore({
            load: vi.fn(async () => ({ available: true, letter: 'T:' })),
            save: vi.fn(async () => { throw new Error('mount controller: invalid Windows drive'); }),
        });
        await store.load();

        await store.choose('Q:');

        expect(get(store)).toMatchObject({ letter: 'T:', saving: false, error: 'mount controller: invalid Windows drive' });
    });
});
