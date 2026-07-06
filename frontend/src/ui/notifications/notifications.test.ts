import { afterEach, describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import EventRow from './EventRow.svelte';
import ToastStack from './ToastStack.svelte';
import TransferRow from './TransferRow.svelte';
import { toasts, type ToastItem } from './toast-store';
import type { NoticeEvent, TransferEvent } from './notif-store';

const noop = () => {};

const stackProps = {
    onDismiss: noop,
    onPauseToast: noop,
    onResumeToast: noop,
    onPauseAll: noop,
    onResumeAll: noop,
};

function makeTransfer(overrides: Partial<TransferEvent> = {}): TransferEvent {
    return {
        kind: 'transfer',
        id: 'xfer:up:1',
        direction: 'up',
        name: 'clip.mp4',
        progress: 40,
        total: 1000,
        bytes: 400,
        speed: 0,
        status: 'active',
        startedAt: 0,
        finishedAt: 0,
        ...overrides,
    };
}

function makeToast(overrides: Partial<ToastItem> = {}): ToastItem {
    return {
        id: 't1',
        level: 'info',
        title: 'Hello',
        body: '',
        sticky: false,
        spinner: false,
        durationMs: 4000,
        expiresAt: 0,
        paused: false,
        ...overrides,
    };
}

afterEach(() => {
    toasts.set([]);
});

describe('TransferRow', () => {
    it('renders progress, size meta, and a cancel control while active', () => {
        const { body } = render(TransferRow, {
            props: { transfer: makeTransfer({ speed: 2048 }), onCancel: noop },
        });

        expect(body).toContain('is-active');
        expect(body).toContain('width:40%');
        expect(body).toContain('400 B / 1000 B');
        expect(body).toContain('2 KB/s');
        expect(body).toContain('notif-row-cancel');
        expect(body).toContain('data-cancel-dir="up"');
    });

    it('renders a terminal status word without a cancel control', () => {
        const { body } = render(TransferRow, {
            props: { transfer: makeTransfer({ status: 'failed', direction: 'down' }) },
        });

        expect(body).toContain('is-failed');
        expect(body).toContain('Failed');
        expect(body).not.toContain('notif-row-cancel');
    });

    it('falls back to a percent readout when the total is unknown', () => {
        const { body } = render(TransferRow, {
            props: { transfer: makeTransfer({ total: 0, progress: 62.4 }) },
        });

        expect(body).toContain('62%');
    });
});

describe('EventRow', () => {
    it('marks error rows with a body as copyable', () => {
        const event: NoticeEvent = {
            kind: 'event',
            id: 'e1',
            level: 'error',
            title: 'Could not join drive',
            body: 'INVITE_HASH_EXPIRED',
            ts: 0,
        };

        const { body } = render(EventRow, { props: { event } });

        expect(body).toContain('data-clipable="1"');
        expect(body).toContain('level-error');
        expect(body).toContain('INVITE_HASH_EXPIRED');
    });

    it('escapes untrusted titles', () => {
        const event: NoticeEvent = {
            kind: 'event',
            id: 'e2',
            level: 'info',
            title: '<img src=x>',
            body: '',
            ts: 0,
        };

        const { body } = render(EventRow, { props: { event } });

        expect(body).not.toContain('<img src=x>');
    });
});

describe('ToastStack', () => {
    it('renders toasts from the store with level styling', () => {
        toasts.set([
            makeToast({ id: 'a', level: 'success', title: 'Folder created' }),
            makeToast({ id: 'b', level: 'error', title: 'Upload failed', body: 'FLOOD_WAIT (420)' }),
        ]);

        const { body } = render(ToastStack, { props: stackProps });

        expect(body).toContain('toast-success');
        expect(body).toContain('Folder created');
        expect(body).toContain('toast-error');
        expect(body).toContain('FLOOD_WAIT (420)');
        expect(body).toContain('role="alert"');
    });

    it('renders the spinner icon for in-progress toasts', () => {
        toasts.set([makeToast({ id: 'c', spinner: true, title: 'Deleting…' })]);

        const { body } = render(ToastStack, { props: stackProps });

        expect(body).toContain('toast-spinner');
    });
});
