// @vitest-environment happy-dom
import { get } from 'svelte/store';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const runtime = vi.hoisted(() => {
    /** The listener the module registered, per event name. */
    const listeners = new Map<string, (payload: unknown) => void>();
    return {
        listeners,
        available: true,
        runtimeEventsAvailable: () => runtime.available,
        onRuntimeEvent: vi.fn((name: string, handler: (payload: unknown) => void) => {
            listeners.set(name, handler);
            return () => listeners.delete(name);
        }),
    };
});
const bell = vi.hoisted(() => ({ cancelTransfersInDirection: vi.fn() }));
const transfers = vi.hoisted(() => ({ downloadRetryFor: vi.fn() }));
const backup = vi.hoisted(() => ({ pausePhotoBackupNow: vi.fn(async () => {}) }));

vi.mock('../api/runtime', () => runtime);
vi.mock('./notif-bell', () => bell);
vi.mock('./transfers', () => transfers);
vi.mock('./photo-backup/controller', () => backup);

import { historyEvents } from '../ui/notifications/notif-store';
import { activeTab } from '../ui/mobile/mobile-shell-store';
import { activateNotificationActions, PAUSE_BACKUP_ACTION, retryTransferAction, stopTransfersAction } from './notification-actions';

/** Presses the button an id names, the way the host would. */
function press(id: unknown): void {
    runtime.listeners.get('android:NotificationAction')?.({ id });
}

/** A failed download row, which is the only kind a retry can refer to. */
function failedDownload(id: string) {
    return { kind: 'transfer' as const, id, direction: 'down' as const, status: 'failed' as const, name: 'holiday.mov', total: 10 };
}

beforeEach(() => {
    runtime.listeners.clear();
    runtime.available = true;
    runtime.onRuntimeEvent.mockClear();
    bell.cancelTransfersInDirection.mockReset();
    transfers.downloadRetryFor.mockReset();
    backup.pausePhotoBackupNow.mockReset();
    historyEvents.set([]);
    activeTab.set('files');
});

describe('notification actions', () => {
    it('pauses the backup from the shade', () => {
        activateNotificationActions();
        press(PAUSE_BACKUP_ACTION.id);
        expect(backup.pausePhotoBackupNow).toHaveBeenCalledOnce();
    });

    it('stops only the direction the button belongs to', () => {
        activateNotificationActions();
        press(stopTransfersAction('up').id);
        expect(bell.cancelTransfersInDirection).toHaveBeenCalledExactlyOnceWith('up');
        press(stopTransfersAction('down').id);
        expect(bell.cancelTransfersInDirection).toHaveBeenLastCalledWith('down');
    });

    it('retries the row the button was posted for', () => {
        const retry = vi.fn();
        transfers.downloadRetryFor.mockReturnValue(retry);
        historyEvents.set([failedDownload('xfer:down:42')] as never);
        activateNotificationActions();
        press(retryTransferAction('xfer:down:42').id);
        expect(transfers.downloadRetryFor).toHaveBeenCalledOnce();
        expect(retry).toHaveBeenCalledOnce();
    });

    it('does nothing for a row that has since gone', () => {
        activateNotificationActions();
        press(retryTransferAction('xfer:down:42').id);
        expect(transfers.downloadRetryFor).not.toHaveBeenCalled();
    });

    it('ignores an id it does not recognise', () => {
        activateNotificationActions();
        press('transfers:stop:sideways');
        press('backup:pause:extra');
        press(undefined);
        expect(bell.cancelTransfersInDirection).not.toHaveBeenCalled();
        expect(backup.pausePhotoBackupNow).not.toHaveBeenCalled();
    });

    it('takes a tap to the work it was about', () => {
        activateNotificationActions();
        runtime.listeners.get('android:OpenRoute')?.({ route: 'transfers' });
        expect(get(activeTab)).toBe('transfers');
    });

    it('leaves the app where it is for a route it does not know', () => {
        activateNotificationActions();
        runtime.listeners.get('android:OpenRoute')?.({ route: 'elsewhere' });
        expect(get(activeTab)).toBe('files');
    });

    it('stops listening once the shell is gone', () => {
        const stop = activateNotificationActions();
        stop();
        expect(runtime.listeners.size).toBe(0);
    });

    it('registers nothing where no host emits these events', () => {
        runtime.available = false;
        activateNotificationActions();
        expect(runtime.onRuntimeEvent).not.toHaveBeenCalled();
    });
});
