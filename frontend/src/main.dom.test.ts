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
vi.mock('../bindings/TDrive/app', () => ({ CheckSystemStatus: vi.fn(async () => 'NEEDS_SETUP') }));

beforeEach(() => {
    vi.resetModules();
    themeLifecycle.disconnect.mockReset();
    themeLifecycle.initialize.mockReset();
    themeLifecycle.initialize.mockReturnValue(themeLifecycle.disconnect);
    document.body.innerHTML = '<div id="app"></div>';
    // Go injects window._wails.environment before the app bundle loads in a
    // real webview; its presence is how the gateway readiness check tells a
    // real Wails window from the plain browser preview. @wailsio/runtime
    // itself reassigns window._wails (`window._wails = window._wails || {}`),
    // so the property must stay writable.
    Object.defineProperty(window, '_wails', {
        configurable: true,
        writable: true,
        value: { environment: { OS: 'darwin', Arch: 'arm64', Debug: true } },
    });
});

afterEach(() => {
    window.onload = null;
    document.body.replaceChildren();
    Reflect.deleteProperty(window, '_wails');
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
