// On a phone the toast stack sits above the tab bar and shows at most two
// (spec 2.6); the module evicts the oldest to keep the queue short. Placement
// itself is CSS; this covers the cap, which is the behavioural half.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';

vi.mock('../../api', async (importOriginal) => {
    const actual = await importOriginal<typeof import('../../api')>();
    return { ...actual, isMobilePlatform: () => true };
});

import { clearAllNotifications, notify } from '../../modules/notifications';
import { toasts } from './toast-store';

beforeEach(() => {
    clearAllNotifications();
});

afterEach(() => {
    clearAllNotifications();
});

describe('toast stack cap on mobile', () => {
    it('keeps at most two toasts, evicting the oldest', () => {
        notify({ title: 'one' });
        notify({ title: 'two' });
        notify({ title: 'three' });

        const list = get(toasts);
        expect(list).toHaveLength(2);
        expect(list.map((toast) => toast.title)).toEqual(['two', 'three']);
    });
});
