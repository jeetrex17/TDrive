import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import EventRow from './EventRow.svelte';
import NotifBell from './NotifBell.svelte';
import ToastStack from './ToastStack.svelte';
import TransferRow from './TransferRow.svelte';
import { historyEvents, notifHoverOpen, notifPanelOpen, type NoticeEvent, type TransferEvent } from './notif-store';
import { toasts, type ToastItem } from './toast-store';

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
    toasts.set([]);
    historyEvents.set([]);
    notifPanelOpen.set(false);
    notifHoverOpen.set(false);
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
    it('cancels a transfer through native button activation', () => {
        const onCancel = vi.fn();
        const host = mountComponent(TransferRow, { transfer: makeTransfer(), onCancel });
        const button = host.querySelector<HTMLButtonElement>('button[aria-label="Cancel transfer"]');
        if (!button) throw new Error('Missing cancel button');
        button.click();
        expect(onCancel).toHaveBeenCalledExactlyOnceWith('up');
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

    it('focuses the deliberate close control and restores the bell after Escape', async () => {
        const host = mountComponent(NotifBell, { onCancelDirection: vi.fn(), onClearHistory: vi.fn() });
        const bell = host.querySelector<HTMLButtonElement>('#notif-bell');
        if (!bell) throw new Error('Expected the notification bell');

        click(bell);
        await tick();
        flushSync();
        const closeButton = document.querySelector<HTMLButtonElement>('.notif-panel-close');
        expect(closeButton).not.toBeNull();
        expect(document.activeElement).toBe(closeButton);

        window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
        flushSync();
        expect(document.querySelector('.notif-panel')).toBeNull();
        expect(document.activeElement).toBe(bell);
    });
});
