import { describe, expect, it } from 'vitest';
import { ScrollMemory } from './scroll-memory';

describe('ScrollMemory', () => {
    it('remembers and returns a view offset', () => {
        const memory = new ScrollMemory(4);
        memory.save('1:photos', 320);
        expect(memory.get('1:photos')).toBe(320);
        expect(memory.get('1:other')).toBeUndefined();
    });

    it('ignores an empty key and clamps and rounds the offset', () => {
        const memory = new ScrollMemory(4);
        memory.save('', 100);
        memory.save('1:a', -40);
        memory.save('1:b', 12.6);
        expect(memory.size).toBe(2);
        expect(memory.get('1:a')).toBe(0);
        expect(memory.get('1:b')).toBe(13);
    });

    it('evicts the oldest entry once over the cap', () => {
        const memory = new ScrollMemory(2);
        memory.save('a', 1);
        memory.save('b', 2);
        memory.save('c', 3);
        expect(memory.size).toBe(2);
        expect(memory.get('a')).toBeUndefined();
        expect(memory.get('b')).toBe(2);
        expect(memory.get('c')).toBe(3);
    });

    it('treats a re-saved key as the newest, sparing it from eviction', () => {
        const memory = new ScrollMemory(2);
        memory.save('a', 1);
        memory.save('b', 2);
        memory.save('a', 10); // a is touched again, so b is now the oldest
        memory.save('c', 3);
        expect(memory.get('a')).toBe(10);
        expect(memory.get('b')).toBeUndefined();
        expect(memory.get('c')).toBe(3);
    });
});
