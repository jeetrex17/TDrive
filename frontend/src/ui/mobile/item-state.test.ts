import { describe, expect, it } from 'vitest';
import {
    ITEM_STATES,
    ITEM_STATE_ORDER,
    itemStateDescriptor,
    opensQueue,
    resolveItemState,
    type ItemState,
} from './item-state';

describe('the state table', () => {
    it('describes every state exactly once', () => {
        expect([...ITEM_STATE_ORDER].sort()).toEqual(Object.keys(ITEM_STATES).sort());
        expect(new Set(ITEM_STATE_ORDER).size).toBe(ITEM_STATE_ORDER.length);
    });

    it('spends accent only where bytes are moving', () => {
        const accent = ITEM_STATE_ORDER.filter((state) => ITEM_STATES[state].tone === 'accent');
        expect(accent).toEqual(['downloading', 'syncing']);
    });

    it('spins exactly the states that are in motion', () => {
        const spinning = ITEM_STATE_ORDER.filter((state) => ITEM_STATES[state].spins);
        expect(spinning).toEqual(['downloading', 'syncing']);
    });

    it('grows the row only for the states a person must act on', () => {
        const tall = ITEM_STATE_ORDER.filter((state) => ITEM_STATES[state].needsExplanation);
        expect(tall).toEqual(['conflict', 'failed']);
    });

    it('gives every state a distinct glyph, so no two read alike', () => {
        const glyphs = ITEM_STATE_ORDER.map((state) => ITEM_STATES[state].glyph);
        expect(new Set(glyphs).size).toBe(glyphs.length);
    });

    it('labels every state without leaning on colour to say it', () => {
        for (const state of ITEM_STATE_ORDER) {
            const info = itemStateDescriptor(state);
            expect(info.label.length).toBeGreaterThan(0);
            expect(info.detail.length).toBeGreaterThan(0);
        }
    });
});

describe('opensQueue', () => {
    it('takes a tap only where the queue has more to say', () => {
        const tappable = ITEM_STATE_ORDER.filter(opensQueue);
        expect(tappable).toEqual(['queued', 'downloading', 'syncing', 'failed']);
    });

    it('leaves the settled states inert', () => {
        expect(opensQueue('online-only')).toBe(false);
        expect(opensQueue('available-offline')).toBe(false);
        // A conflict is resolved on the file, not in the transfer queue.
        expect(opensQueue('conflict')).toBe(false);
    });
});

describe('resolveItemState', () => {
    it('reads stored state when nothing is in flight', () => {
        expect(resolveItemState({})).toBe<ItemState>('online-only');
        expect(resolveItemState({ offline: true })).toBe<ItemState>('available-offline');
    });

    it('separates the two directions of movement', () => {
        expect(resolveItemState({ transfer: { status: 'active', direction: 'down' } })).toBe<ItemState>('downloading');
        expect(resolveItemState({ transfer: { status: 'active', direction: 'up' } })).toBe<ItemState>('syncing');
    });

    it('folds paused into queued, because the row means the same thing either way', () => {
        expect(resolveItemState({ transfer: { status: 'queued', direction: 'down' } })).toBe<ItemState>('queued');
        expect(resolveItemState({ transfer: { status: 'paused', direction: 'up' } })).toBe<ItemState>('queued');
    });

    it('surfaces a failure over the stored state it would otherwise show', () => {
        expect(resolveItemState({ transfer: { status: 'failed', direction: 'up' }, offline: true }))
            .toBe<ItemState>('failed');
    });

    it('lets a finished transfer fall back to what the file now is', () => {
        expect(resolveItemState({ transfer: { status: 'done', direction: 'down' }, offline: true }))
            .toBe<ItemState>('available-offline');
        expect(resolveItemState({ transfer: { status: 'done', direction: 'up' } })).toBe<ItemState>('online-only');
    });

    it('puts a conflict above everything, including an active transfer', () => {
        expect(resolveItemState({ conflicted: true })).toBe<ItemState>('conflict');
        expect(resolveItemState({ conflicted: true, transfer: { status: 'active', direction: 'up' } }))
            .toBe<ItemState>('conflict');
        expect(resolveItemState({ conflicted: true, offline: true })).toBe<ItemState>('conflict');
    });
});
