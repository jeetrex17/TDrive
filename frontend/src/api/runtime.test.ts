import { afterEach, describe, expect, it, vi } from 'vitest';
import {
    BackendInvocationError,
    invokeBackend,
    invokeRuntimeAsync,
    RuntimeInvocationError,
} from './gateway';

// runtime.ts is a thin wrapper over @wailsio/runtime's Events/Browser/System/
// Window modules; mock those instead of the removed window.runtime bridge.
const eventsOn = vi.hoisted(() => vi.fn());
vi.mock('@wailsio/runtime', () => ({
    Events: { On: eventsOn },
    Browser: { OpenURL: vi.fn() },
    System: { Environment: vi.fn() },
    Window: {
        Fullscreen: vi.fn(),
        UnFullscreen: vi.fn(),
        IsFullscreen: vi.fn(),
        SetBackgroundColour: vi.fn(),
    },
}));

import {
    isFullscreen,
    onRuntimeEvent,
    RuntimeUnavailableError,
    waitForGatewayReady,
} from './runtime';

afterEach(() => {
    vi.unstubAllGlobals();
    eventsOn.mockReset();
});

describe('typed gateway runtime boundary', () => {
    it('normalizes synchronous generated bindings into promises', async () => {
        await expect(invokeBackend(() => 'ready')).resolves.toBe('ready');
    });

    it('wraps a synchronous generated binding failure with its original cause', async () => {
        const cause = new Error('native bridge stopped');
        let failure: unknown;

        try {
            await invokeBackend(() => {
                throw cause;
            });
        } catch (error) {
            failure = error;
        }

        expect(failure).toBeInstanceOf(BackendInvocationError);
        if (failure instanceof BackendInvocationError) {
            expect(failure.cause).toBe(cause);
        }
    });

    it('preserves structured asynchronous backend rejections as the cause', async () => {
        const cause = {
            code: 'permission_denied',
            details: { requestId: 'req-24' },
        };
        let failure: unknown;

        try {
            await invokeBackend(async () => Promise.reject(cause));
        } catch (error) {
            failure = error;
        }

        expect(failure).toBeInstanceOf(BackendInvocationError);
        if (failure instanceof BackendInvocationError) {
            expect(failure.cause).toBe(cause);
        }
    });

    it('wraps asynchronous runtime rejections with their original cause', async () => {
        const cause = new Error('native runtime stopped');
        let failure: unknown;

        try {
            await invokeRuntimeAsync('Environment', {}, () => Promise.reject(cause));
        } catch (error) {
            failure = error;
        }

        expect(failure).toBeInstanceOf(RuntimeInvocationError);
        if (failure instanceof RuntimeInvocationError) {
            expect(failure.method).toBe('Environment');
            expect(failure.cause).toBe(cause);
        }
    });

    it('times out readiness and rejects native calls predictably when Wails is absent', async () => {
        vi.stubGlobal('window', {});

        await expect(waitForGatewayReady(0)).resolves.toBe(false);
        await expect(isFullscreen()).rejects.toBeInstanceOf(RuntimeUnavailableError);
    });

    it('forwards each typed event tuple and tears down its native listener once', () => {
        const stop = vi.fn();
        let listener: ((event: { name: string; data: unknown }) => void) | undefined;
        eventsOn.mockImplementation((eventName: string, callback: typeof listener) => {
            expect(eventName).toBe('preview_progress');
            listener = callback;
            return stop;
        });
        const callback = vi.fn();

        const unsubscribe = onRuntimeEvent('preview_progress', callback);
        listener?.({ name: 'preview_progress', data: [42, 75] });
        unsubscribe();
        unsubscribe();

        expect(callback).toHaveBeenCalledExactlyOnceWith(42, 75);
        expect(stop).toHaveBeenCalledOnce();
    });
});
