import { get } from 'svelte/store';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { mountSelection } from './mount-selection-store';

const drives = [
    { id: 20, title: 'Project', kind: 'shared' as const },
    { id: 10, title: 'My files', kind: 'personal' as const },
    { id: 30, title: 'Family', kind: 'shared' as const },
];

function fakeStorage(): Storage {
    const values = new Map<string, string>();
    return {
        get length() {
            return values.size;
        },
        clear: () => values.clear(),
        getItem: (key: string) => values.get(key) ?? null,
        key: (index: number) => Array.from(values.keys())[index] ?? null,
        removeItem: (key: string) => values.delete(key),
        setItem: (key: string, value: string) => values.set(key, value),
    };
}

afterEach(() => {
    mountSelection.reset();
    vi.unstubAllGlobals();
});

describe('mount selection store', () => {
    it('orders personal first and selects every drive on first open', () => {
        mountSelection.open(drives, vi.fn());

        expect(get(mountSelection.state)).toEqual({
            open: true,
            drives: [drives[1], drives[0], drives[2]],
            selectedIds: [10, 20, 30],
        });
    });

    it('updates selection immutably and remembers a confirmed subset for this session', () => {
        const confirm = vi.fn();
        mountSelection.open(drives, confirm);
        const before = get(mountSelection.state);

        mountSelection.toggle(20);

        const after = get(mountSelection.state);
        expect(after).not.toBe(before);
        expect(after.selectedIds).not.toBe(before.selectedIds);
        expect(before.selectedIds).toEqual([10, 20, 30]);
        expect(after.selectedIds).toEqual([10, 30]);

        mountSelection.confirm();
        expect(confirm).toHaveBeenCalledWith([10, 30]);
        expect(get(mountSelection.state).open).toBe(false);

        mountSelection.open(drives, vi.fn());
        expect(get(mountSelection.state).selectedIds).toEqual([10, 30]);
    });

    it('keeps a confirmed subset out of browser storage', () => {
        const storage = fakeStorage();
        const setItem = vi.spyOn(storage, 'setItem');
        vi.stubGlobal('localStorage', storage);

        mountSelection.open(drives, vi.fn());
        mountSelection.toggle(20);
        mountSelection.confirm();

        expect(setItem).not.toHaveBeenCalled();
    });

    it('does not confirm an empty selection and cancel has no side effects', () => {
        const confirm = vi.fn();
        mountSelection.open([drives[1]], confirm);
        mountSelection.toggle(10);

        mountSelection.confirm();
        expect(confirm).not.toHaveBeenCalled();
        expect(get(mountSelection.state).open).toBe(true);

        mountSelection.close();
        expect(confirm).not.toHaveBeenCalled();
        expect(get(mountSelection.state).open).toBe(false);
    });
});
