//
// Browser-mode behavior for the trash surface: what the list shows, that a
// restore leaves the list, and that neither irreversible path runs without the
// confirm answering for it. The API module is the only thing mocked, so the
// controller, both dialogs and the real ModalShell wiring are exercised.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import type { TrashEntry } from '../../api/trash';

const listTrash = vi.fn();
const restoreFromTrash = vi.fn();
const deleteFromTrashPermanently = vi.fn();
const emptyTrash = vi.fn();

vi.mock('../../api/trash', () => ({
    listTrash: () => listTrash(),
    restoreFromTrash: (objectId: string) => restoreFromTrash(objectId),
    deleteFromTrashPermanently: (objectId: string) => deleteFromTrashPermanently(objectId),
    emptyTrash: () => emptyTrash(),
}));

// modal-a11y only treats elements with a layout box (offsetParent) as
// focusable; happy-dom has no layout, so approximate it with the DOM parent.
Object.defineProperty(HTMLElement.prototype, 'offsetParent', {
    configurable: true,
    get() {
        return this.parentElement;
    },
});

const { closeTrash, confirmTrashAction, openTrash } = await import('../../modules/trash/controller');
const TrashModal = (await import('./TrashModal.svelte')).default;
const TrashConfirmModal = (await import('./TrashConfirmModal.svelte')).default;

const HOUR = 3_600_000;
const DAY = 24 * HOUR;
const now = Date.now();
// listTrash answers with normalized entries, so these are the shape the
// controller actually receives -- normalization has its own unit tests.
const rows: TrashEntry[] = [
    { objectId: 'd:9c1', kind: 'folder', name: 'Tax returns', parentPath: 'Documents', size: 0, deletedAt: now - 3 * DAY, purgeAfter: now + 27 * DAY + HOUR },
    { objectId: 'f:2615', kind: 'file', name: 'IMG_0042.HEIC', parentPath: 'Trips/Iceland', size: 4_812_000, deletedAt: now - 60_000, purgeAfter: now + 29 * DAY + HOUR },
];

let host: HTMLElement;
let confirmHost: HTMLElement;
let panel: Record<string, unknown>;
let confirmPanel: Record<string, unknown>;

async function settle(): Promise<void> {
    flushSync();
    await Promise.resolve();
    await tick();
    flushSync();
    await tick();
    flushSync();
}

function rowNames(): string[] {
    return [...host.querySelectorAll('.trash-row-name')].map((node) => node.textContent?.trim() ?? '');
}

function button(root: HTMLElement, label: string): HTMLButtonElement {
    const match = [...root.querySelectorAll('button')].find((node) => (
        node.getAttribute('aria-label') === label || node.textContent?.trim() === label
    ));
    if (!match) throw new Error(`no "${label}" button in ${root.id}`);
    return match;
}

async function click(root: HTMLElement, label: string): Promise<void> {
    button(root, label).click();
    await settle();
}

beforeEach(async () => {
    listTrash.mockResolvedValue(rows.map((row) => ({ ...row })));
    restoreFromTrash.mockResolvedValue({ ok: true });
    deleteFromTrashPermanently.mockResolvedValue({ ok: true });
    emptyTrash.mockResolvedValue({ ok: true });

    host = document.createElement('div');
    host.id = 'trash-modal';
    confirmHost = document.createElement('div');
    confirmHost.id = 'trash-confirm-modal';
    document.body.append(host, confirmHost);
    panel = mount(TrashModal, { target: host });
    confirmPanel = mount(TrashConfirmModal, { target: confirmHost, props: { onConfirm: confirmTrashAction } });

    openTrash();
    await settle();
});

afterEach(async () => {
    closeTrash();
    flushSync();
    await unmount(panel);
    await unmount(confirmPanel);
    host.remove();
    confirmHost.remove();
    vi.clearAllMocks();
});

describe('trash surface', () => {
    it('lists the newest deletion first, with where it came from', async () => {
        expect(rowNames()).toEqual(['IMG_0042.HEIC', 'Tax returns']);
        expect(host.textContent).toContain('Trips/Iceland');
        expect(host.textContent).toContain('29 days left');
        expect(host.textContent).toContain('2 items');
    });

    it('restores an item straight from its row and drops it from the list', async () => {
        await click(host, 'Restore IMG_0042.HEIC');
        expect(restoreFromTrash).toHaveBeenCalledWith('f:2615');
        expect(rowNames()).toEqual(['Tax returns']);
    });

    it("keeps a refused restore's row and repeats the backend's reason", async () => {
        restoreFromTrash.mockResolvedValue({ ok: false, error: { code: 'operation_failed', message: 'The original folder is gone.' } });
        await click(host, 'Restore IMG_0042.HEIC');
        expect(host.querySelector('[role="alert"]')?.textContent?.trim()).toBe('The original folder is gone.');
        expect(rowNames()).toHaveLength(2);
    });

    it('never deletes permanently without the confirm answering for it', async () => {
        await click(host, 'Delete IMG_0042.HEIC permanently');
        expect(deleteFromTrashPermanently).not.toHaveBeenCalled();
        expect(confirmHost.textContent).toContain('IMG_0042.HEIC');

        await click(confirmHost, 'Cancel');
        expect(deleteFromTrashPermanently).not.toHaveBeenCalled();
        expect(rowNames()).toHaveLength(2);

        await click(host, 'Delete IMG_0042.HEIC permanently');
        await click(confirmHost, 'Delete permanently');
        expect(deleteFromTrashPermanently).toHaveBeenCalledWith('f:2615');
        expect(rowNames()).toEqual(['Tax returns']);
    });

    it('never empties the trash without the confirm answering for it', async () => {
        await click(host, 'Empty trash');
        expect(emptyTrash).not.toHaveBeenCalled();
        expect(confirmHost.textContent).toContain('2 items');

        await click(confirmHost, 'Empty trash');
        expect(emptyTrash).toHaveBeenCalledTimes(1);
        expect(rowNames()).toHaveLength(0);
        expect(host.textContent).toContain('Nothing in the trash');
        // Nothing left to empty, so the control is gone, not disabled.
        expect([...host.querySelectorAll('button')].some((node) => node.textContent?.trim() === 'Empty trash')).toBe(false);
    });

    it('offers a way back when the trash cannot be read at all', async () => {
        closeTrash();
        listTrash.mockRejectedValueOnce(new Error('Telegram is unreachable.'));
        openTrash();
        await settle();
        expect(host.textContent).toContain('The trash could not be opened');

        await click(host, 'Try again');
        expect(rowNames()).toEqual(['IMG_0042.HEIC', 'Tax returns']);
    });
});
