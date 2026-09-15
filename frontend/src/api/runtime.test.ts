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
vi.mock('@wailsio/runtime', () => {
    // The real platform helpers read the OS Go injects into window._wails.environment.
    const os = () => (typeof window === 'undefined' ? undefined : window._wails?.environment?.OS);
    return {
        Events: { On: eventsOn },
        Browser: { OpenURL: vi.fn() },
        System: {
            Environment: vi.fn(),
            IsIOS: () => os() === 'ios',
            IsAndroid: () => os() === 'android',
            IsMobile: () => os() === 'ios' || os() === 'android',
        },
        Window: {
            Fullscreen: vi.fn(),
            UnFullscreen: vi.fn(),
            IsFullscreen: vi.fn(),
            SetBackgroundColour: vi.fn(),
        },
    };
});

import {
    fullscreenAvailable,
    isAndroidPlatform,
    isFullscreen,
    isIOSPlatform,
    isMobilePlatform,
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

    it('treats a native bridge without an injected environment as ready and hydrates it', async () => {
        // The Android and iOS hosts in Wails 3 beta.22 install their bridge but
        // never run the desktop-only script that sets window._wails.environment.
        const environment = vi.fn().mockResolvedValue({ OS: 'android', Arch: 'arm64', Debug: true });
        const runtime = await import('@wailsio/runtime');
        vi.mocked(runtime.System.Environment).mockImplementation(environment);
        vi.stubGlobal('window', {
            wails: { invoke: vi.fn(), platform: () => 'android' },
            location: { search: '' },
            setTimeout: globalThis.setTimeout,
        });

        expect(isAndroidPlatform()).toBe(true);
        expect(isMobilePlatform()).toBe(true);
        await expect(waitForGatewayReady(0)).resolves.toBe(true);

        expect(environment).toHaveBeenCalledOnce();
        expect(window._wails?.environment).toEqual({ OS: 'android', Arch: 'arm64', Debug: true });
        expect(isIOSPlatform()).toBe(false);
    });

    it('keeps the bridge fallbacks when the environment call fails', async () => {
        const runtime = await import('@wailsio/runtime');
        vi.mocked(runtime.System.Environment).mockRejectedValue(new Error('bridge down'));
        vi.spyOn(console, 'warn').mockImplementation(() => {});
        vi.stubGlobal('window', {
            webkit: { messageHandlers: { external: { postMessage: vi.fn() } } },
            location: { search: '' },
            setTimeout: globalThis.setTimeout,
        });

        await expect(waitForGatewayReady(0)).resolves.toBe(true);
        expect(window._wails?.environment).toBeUndefined();
        expect(isMobilePlatform()).toBe(false);
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

describe('platform helpers', () => {
    it('report a desktop while the gateway is not ready', () => {
        vi.stubGlobal('window', { location: { search: '' } });

        expect(isMobilePlatform()).toBe(false);
        expect(isIOSPlatform()).toBe(false);
        expect(isAndroidPlatform()).toBe(false);
        expect(fullscreenAvailable()).toBe(false);
    });

    it('read the injected OS once the gateway is ready', () => {
        vi.stubGlobal('window', { _wails: { environment: { OS: 'android' } }, location: { search: '' } });

        expect(isMobilePlatform()).toBe(true);
        expect(isAndroidPlatform()).toBe(true);
        expect(isIOSPlatform()).toBe(false);
        expect(fullscreenAvailable()).toBe(false);

        vi.stubGlobal('window', { _wails: { environment: { OS: 'darwin' } }, location: { search: '' } });

        expect(isMobilePlatform()).toBe(false);
        expect(fullscreenAvailable()).toBe(true);
    });

    it('honour the ?mobile browser-preview override ahead of the gateway', () => {
        vi.stubGlobal('window', { location: { search: '?mobile=1' } });

        expect(isMobilePlatform()).toBe(true);
        expect(isIOSPlatform()).toBe(false);
        expect(isAndroidPlatform()).toBe(false);
        expect(fullscreenAvailable()).toBe(false);

        vi.stubGlobal('window', { location: { search: '?mobile=ios' } });

        expect(isIOSPlatform()).toBe(true);
        expect(isAndroidPlatform()).toBe(false);

        vi.stubGlobal('window', { _wails: { environment: { OS: 'darwin' } }, location: { search: '?mobile=android' } });

        expect(isMobilePlatform()).toBe(true);
        expect(isAndroidPlatform()).toBe(true);
        expect(isIOSPlatform()).toBe(false);
    });
});
