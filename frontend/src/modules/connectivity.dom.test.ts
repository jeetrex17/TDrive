import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';
import { toasts } from '../ui/notifications/toast-store';
import { activateConnectivityWatch, isOffline } from './connectivity';

const OFFLINE_TOAST_ID = 'connectivity-offline';

function setOnLine(value: boolean): void {
    Object.defineProperty(window.navigator, 'onLine', { value, configurable: true });
}

function currentToast() {
    return get(toasts).find((toast) => toast.id === OFFLINE_TOAST_ID);
}

let deactivate: () => void = () => {};

beforeEach(() => {
    setOnLine(true);
    toasts.set([]);
});

afterEach(() => {
    deactivate();
    deactivate = () => {};
    toasts.set([]);
    setOnLine(true);
});

describe('connectivity watch', () => {
    it('says nothing while the link is up', () => {
        deactivate = activateConnectivityWatch();

        expect(currentToast()).toBeUndefined();
    });

    it('reports a link that is already down when it starts', () => {
        setOnLine(false);

        deactivate = activateConnectivityWatch();

        const toast = currentToast();
        expect(toast?.level).toBe('warning');
        expect(toast?.sticky).toBe(true);
    });

    it('replaces the offline toast in place when the link returns', () => {
        deactivate = activateConnectivityWatch();
        window.dispatchEvent(new Event('offline'));
        expect(currentToast()?.level).toBe('warning');

        window.dispatchEvent(new Event('online'));

        const toast = currentToast();
        expect(toast?.level).toBe('success');
        expect(toast?.sticky).toBe(false);
        // One entry, not two: the stack must not jump while the two swap over.
        expect(get(toasts).filter((entry) => entry.id === OFFLINE_TOAST_ID)).toHaveLength(1);
    });

    it('offers a refresh that clears the toast it came from', async () => {
        const triggerRefresh = vi.fn(async () => {});
        const { configureAppActions } = await import('./app-actions');
        configureAppActions({ triggerRefresh } as never);

        deactivate = activateConnectivityWatch();
        window.dispatchEvent(new Event('offline'));
        window.dispatchEvent(new Event('online'));

        currentToast()?.action?.run();

        expect(triggerRefresh).toHaveBeenCalledTimes(1);
        expect(currentToast()).toBeUndefined();
    });

    it('stops reporting once deactivated', () => {
        deactivate = activateConnectivityWatch();
        deactivate();
        deactivate = () => {};

        window.dispatchEvent(new Event('offline'));

        expect(currentToast()).toBeUndefined();
    });

    it('treats only an explicit false as offline', () => {
        setOnLine(true);
        expect(isOffline()).toBe(false);
        setOnLine(false);
        expect(isOffline()).toBe(true);
    });
});
