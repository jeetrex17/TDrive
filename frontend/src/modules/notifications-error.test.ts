import { beforeEach, describe, expect, it } from 'vitest';
import { get } from 'svelte/store';
import {
    clearAllNotifications,
    notifyAppError,
} from './notifications';
import { toasts } from '../ui/notifications/toast-store';

beforeEach(() => clearAllNotifications());

describe('notifyAppError', () => {
    it('publishes only redacted user copy as a sticky error', () => {
        const notificationId = notifyAppError(
            new Error('token=super-secret failed at /Users/alice/TDrive/cache.db'),
            { id: 'safe-error', title: 'Could not open file', source: 'backend' },
        );

        expect(notificationId).toBe('safe-error');
        expect(get(toasts)).toEqual([
            expect.objectContaining({
                id: 'safe-error',
                level: 'error',
                title: 'Could not open file',
                body: 'token=[redacted] failed at [local path]',
                sticky: true,
            }),
        ]);
        expect(JSON.stringify(get(toasts))).not.toContain('super-secret');
        expect(JSON.stringify(get(toasts))).not.toContain('/Users/alice');
    });

    it('uses the structured AppError title and operation-code message by default', () => {
        const failure = Object.assign(new Error('backend invocation failed'), {
                    cause: { code: 'permission_denied', message: 'localized backend detail' },
                });

        notifyAppError(failure, { id: 'permission', source: 'backend' });

        expect(get(toasts)[0]).toMatchObject({
            title: 'Permission denied',
            body: "You don't have permission to do that.",
        });
    });

    it('contains a synchronous notification renderer failure', () => {
        let rendererActive = false;
        const unsubscribe = toasts.subscribe(() => {
            if (rendererActive) throw new Error('toast renderer failed');
        });
        rendererActive = true;

        try {
            expect(notifyAppError(new Error('screen failed'), { id: 'contained' })).toBeNull();
        } finally {
            rendererActive = false;
            unsubscribe();
        }
    });
});
