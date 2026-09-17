import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import EventRow from './EventRow.svelte';
import NotifBell from './NotifBell.svelte';
import ToastStack from './ToastStack.svelte';
import TransferRow from './TransferRow.svelte';
import { historyEvents, notifPanelOpen, type NoticeEvent, type TransferEvent } from './notif-store';
import { toasts, type ToastItem } from './toast-store';
import { cancelSingleUpload } from '../../modules/notif-bell';

// The row talks to the backend itself when it can stop just this one upload;
// the rest of the module stays real so the stores behave as they do in the app.
vi.mock('../../modules/notif-bell', async (importOriginal) => {
    const actual = await importOriginal<typeof import('../../modules/notif-bell')>();
    return { ...actual, cancelSingleUpload: vi.fn() };
});

let mounts: Record<string, unknown>[] = [];
let hosts: HTMLElement[] = [];
let clipboardDescriptor: PropertyDescriptor | undefined;

function mountComponent(component: typeof EventRow, props: { event: NoticeEvent }): HTMLElement;
function mountComponent(component: typeof TransferRow, props: { transfer: TransferEvent; onCancel?: () => void }): HTMLElement;
function mountComponent(component: typeof ToastStack, props: {
    onDismiss: (id: string) => void;
    onPauseToast: () => void;
    onResumeToast: () => void;
    onPauseAll: () => void;
    onResumeAll: () => void;
}): HTMLElement;
function mountComponent(component: typeof NotifBell, props: { onCancelDirection: () => void; onClearHistory: () => void }): HTMLElement;
function mountComponent(component: unknown, props: Record<string, unknown>): HTMLElement {
    const host = document.createElement('div');
    document.body.appendChild(host);
    hosts.push(host);
    mounts.push(mount(component as never, { target: host, props }) as Record<string, unknown>);
    return host;
}

function click(element: Element): void {
    element.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
    flushSync();
}

function makeTransfer(overrides: Partial<TransferEvent> = {}): TransferEvent {
    return {
        kind: 'transfer',
        id: 'xfer:up:1',
        direction: 'up',
        name: 'project.zip',
        progress: 40.4,
        total: 1_000,
        bytes: 404,
        speed: 0,
        status: 'active',
        startedAt: 0,
        finishedAt: 0,
        ...overrides,
    };
}

function makeToast(overrides: Partial<ToastItem> = {}): ToastItem {
    return {
        id: 'toast-1',
        level: 'error',
        title: 'Upload failed',
        body: 'FLOOD_WAIT (420)',
        sticky: true,
        spinner: false,
        durationMs: 0,
        expiresAt: 0,
        paused: false,
        remainingMs: 0,
        ...overrides,
    };
}

afterEach(async () => {
    vi.clearAllMocks();
    toasts.set([]);
    historyEvents.set([]);
    notifPanelOpen.set(false);
    flushSync();
    await Promise.all(mounts.map((app) => unmount(app)));
    mounts = [];
    hosts.forEach((host) => host.remove());
    hosts = [];
    if (clipboardDescriptor) Object.defineProperty(navigator, 'clipboard', clipboardDescriptor);
    else Reflect.deleteProperty(navigator, 'clipboard');
    clipboardDescriptor = undefined;
});

describe('notification interaction controls', () => {
    it('stops the whole direction from an upload row on desktop', () => {
        // The narrow per-file cancel is the phone's, because the phone also has
        // a Cancel all beside the section title. The bell's rows are its only
        // cancel, and only three uploads run at once, so narrowing it here
        // would leave a twenty-file batch with no way to stop.
        const onCancel = vi.fn();
        const host = mountComponent(TransferRow, { transfer: makeTransfer(), onCancel });
        const button = host.querySelector<HTMLButtonElement>('button[aria-label="Cancel all uploads"]');
        if (!button) throw new Error('Missing cancel button');
        button.click();
        expect(onCancel).toHaveBeenCalledExactlyOnceWith('up');
        expect(cancelSingleUpload).not.toHaveBeenCalled();
    });
    it('cancels the whole direction for a row the backend cannot stop on its own', () => {
        for (const id of ['xfer:down:file:42', 'xfer:up:import']) {
            const direction = id.startsWith('xfer:up:') ? 'up' as const : 'down' as const;
            const onCancel = vi.fn();
            const host = mountComponent(TransferRow, { transfer: makeTransfer({ id, direction }), onCancel });
            const label = direction === 'down' ? 'Cancel active download' : 'Cancel all uploads';
            const button = host.querySelector<HTMLButtonElement>(`button[aria-label="${label}"]`);
            if (!button) throw new Error('Missing cancel button');
            button.click();
            expect(onCancel).toHaveBeenCalledExactlyOnceWith(direction);
            expect(cancelSingleUpload).not.toHaveBeenCalled();
        }
    });
    it('copies error details only from the explicit Copy details button', async () => {
        clipboardDescriptor = Object.getOwnPropertyDescriptor(navigator, 'clipboard');
        const writeText = vi.fn().mockResolvedValue(undefined);
        Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
        const host = mountComponent(EventRow, {
            event: {
                kind: 'event',
                id: 'error-1',
                level: 'error',
                title: 'Could not join drive',
                body: 'INVITE_HASH_EXPIRED',
                ts: 0,
            },
        });
        const row = host.querySelector('.notif-row');
        const copyButton = host.querySelector<HTMLButtonElement>('.notif-row-copy');
        if (!row || !copyButton) throw new Error('Expected an error row with a copy control');

        click(row);
        expect(writeText).not.toHaveBeenCalled();

        click(copyButton);
        await Promise.resolve();
        flushSync();
        expect(writeText).toHaveBeenCalledWith('Could not join drive\nINVITE_HASH_EXPIRED');
    });

    it('dismisses an error toast only with its close control', () => {
        const onDismiss = vi.fn();
        toasts.set([makeToast()]);
        const host = mountComponent(ToastStack, {
            onDismiss,
            onPauseToast: vi.fn(),
            onResumeToast: vi.fn(),
            onPauseAll: vi.fn(),
            onResumeAll: vi.fn(),
        });
        const toast = host.querySelector('.toast');
        const closeButton = host.querySelector<HTMLButtonElement>('.toast-close');
        if (!toast || !closeButton) throw new Error('Expected a toast with a close control');

        click(toast);
        expect(onDismiss).not.toHaveBeenCalled();

        click(closeButton);
        expect(onDismiss).toHaveBeenCalledWith('toast-1');
    });

    it('exposes transfer progress without creating a live region for each tick', () => {
        const host = mountComponent(TransferRow, { transfer: makeTransfer() });
        const progress = host.querySelector<HTMLElement>('[role="progressbar"]');
        if (!progress) throw new Error('Expected a progressbar');

        expect(progress.getAttribute('aria-label')).toBe('Uploading project.zip');
        expect(progress.getAttribute('aria-valuemin')).toBe('0');
        expect(progress.getAttribute('aria-valuemax')).toBe('100');
        expect(progress.getAttribute('aria-valuenow')).toBe('40');
        expect(progress.hasAttribute('aria-live')).toBe(false);
    });

    it('opens from the keyboard and restores the bell after Escape', () => {
        const host = mountComponent(NotifBell, { onCancelDirection: vi.fn(), onClearHistory: vi.fn() });
        const bell = host.querySelector<HTMLButtonElement>('#notif-bell');
        if (!bell) throw new Error('Expected the notification bell');

        bell.focus();
        bell.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }));
        flushSync();
        expect(document.querySelector('.notif-panel')).not.toBeNull();
        expect(document.querySelector('.notif-panel-close')).toBeNull();

        window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
        flushSync();
        expect(document.querySelector('.notif-panel')).toBeNull();
        expect(document.activeElement).toBe(bell);
    });
});
