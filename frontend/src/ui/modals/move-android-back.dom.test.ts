// On a phone, Android BACK inside the Move sheet walks up one folder before it
// leaves the sheet. While the browse is drilled in, the sheet registers a
// higher-priority BACK target whose close steps up rather than dismissing.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import { get } from 'svelte/store';

vi.mock('../../api', () => ({ isMobilePlatform: () => true }));

import MoveModal from './MoveModal.svelte';
import { moveBrowse, moveModal, resetMoveBrowse } from './move-modal-store';
import { closeTopSheet } from './sheet-stack';

Object.defineProperty(HTMLElement.prototype, 'offsetParent', {
    configurable: true,
    get() {
        return this.parentElement;
    },
});

let host: HTMLElement;
let app: Record<string, unknown> | null = null;
const onBack = vi.fn();

async function settle(): Promise<void> {
    flushSync();
    await tick();
    await Promise.resolve();
    flushSync();
}

beforeEach(() => {
    host = document.createElement('div');
    host.id = 'move-modal';
    document.body.appendChild(host);
    app = mount(MoveModal, {
        target: host,
        props: { onOpenFolder: vi.fn(), onCrumb: vi.fn(), onBack, onConfirm: vi.fn() },
    });
    flushSync();
});

afterEach(async () => {
    moveModal.close();
    resetMoveBrowse('');
    flushSync();
    if (app) await unmount(app);
    app = null;
    host.remove();
    onBack.mockReset();
});

describe('Move sheet Android BACK', () => {
    it('steps up a folder instead of closing while drilled in', async () => {
        moveModal.open({ title: 'Move "a.txt"' });
        resetMoveBrowse('');
        await settle();
        // Drilling happens after the sheet is open, so its step-up target sits
        // above the dialog's own BACK claim and is consumed first.
        moveBrowse.update((browse) => ({ ...browse, path: [{ id: 'd:docs', name: 'Docs' }] }));
        await settle();

        // BACK reaches the step-up target the sheet registered for its depth.
        expect(closeTopSheet()).toBe(true);
        await settle();

        expect(onBack).toHaveBeenCalledTimes(1);
        // The sheet is still open: a step up is not a dismissal.
        expect(get(moveModal.state).open).toBe(true);
    });

    it('registers no step-up target at the root', async () => {
        moveModal.open({ title: 'Move "a.txt"' });
        resetMoveBrowse('');
        await settle();

        // Draining the step-up target, if any, must not call onBack at the root.
        closeTopSheet();
        await settle();
        expect(onBack).not.toHaveBeenCalled();
    });
});
