import { afterEach, describe, expect, it, vi } from 'vitest';
import {
    BackendInvocationError,
    invokeBackend,
    invokeRuntimeAsync,
    RuntimeInvocationError,
} from './gateway';
import {
    isFullscreen,
    onRuntimeEvent,
    RuntimeUnavailableError,
    waitForGatewayReady,
} from './runtime';

afterEach(() => {
    vi.unstubAllGlobals();
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
        let listener: ((messageId: unknown, percent: unknown) => void) | undefined;
        const eventsOn = vi.fn((eventName: string, callback: (messageId: unknown, percent: unknown) => void) => {
            expect(eventName).toBe('preview_progress');
            listener = callback;
            return stop;
        });
        const callback = vi.fn();
        vi.stubGlobal('window', { runtime: { EventsOn: eventsOn } });

        const unsubscribe = onRuntimeEvent('preview_progress', callback);
        listener?.(42, 75);
        unsubscribe();
        unsubscribe();

        expect(callback).toHaveBeenCalledExactlyOnceWith(42, 75);
        expect(stop).toHaveBeenCalledOnce();
    });
});
