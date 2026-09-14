import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const themeLifecycle = vi.hoisted(() => ({
    disconnect: vi.fn(),
    initialize: vi.fn(),
}));

vi.mock('./ui/theme/theme-controller', () => ({
    initializeTheme: themeLifecycle.initialize,
}));

vi.mock('./ui/theme/native-theme', () => ({ initializeNativeTheme: vi.fn(async () => vi.fn()) }));
vi.mock('./modules/app-shell', () => ({
    mountApplication: vi.fn(() => ({ destroy: vi.fn(async () => {}) })),
}));




beforeEach(() => {
    vi.resetModules();
    themeLifecycle.disconnect.mockReset();
    themeLifecycle.initialize.mockReset();
    themeLifecycle.initialize.mockReturnValue(themeLifecycle.disconnect);
        document.body.innerHTML = '<div id="app"></div>';
        Object.defineProperty(window, 'runtime', {
            configurable: true,
            value: { EventsOn: vi.fn(() => vi.fn()) },
        });
        Object.defineProperty(window, 'go', {
            configurable: true,
            value: { main: { App: { CheckSystemStatus: vi.fn(async () => 'NEEDS_SETUP') } } },
        });
});

afterEach(() => {
    window.onload = null;
        document.body.replaceChildren();
        Reflect.deleteProperty(window, 'runtime');
        Reflect.deleteProperty(window, 'go');
});

describe('application appearance lifecycle', () => {
    it('releases the theme controller once during beforeunload', async () => {
        await import('./main');

        expect(themeLifecycle.initialize).toHaveBeenCalledOnce();

        window.dispatchEvent(new Event('beforeunload'));
        window.dispatchEvent(new Event('beforeunload'));

        expect(themeLifecycle.disconnect).toHaveBeenCalledOnce();
    }, 10_000);
});
