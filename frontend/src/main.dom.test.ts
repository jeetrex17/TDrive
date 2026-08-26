import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const themeLifecycle = vi.hoisted(() => ({
    disconnect: vi.fn(),
    initialize: vi.fn(),
}));

vi.mock('./ui/theme/theme-controller', () => ({
    initializeTheme: themeLifecycle.initialize,
}));

vi.mock('./ui/theme/native-theme', () => ({
    initializeNativeTheme: vi.fn(async () => vi.fn()),
}));

beforeEach(() => {
    vi.resetModules();
    themeLifecycle.disconnect.mockReset();
    themeLifecycle.initialize.mockReset();
    themeLifecycle.initialize.mockReturnValue(themeLifecycle.disconnect);
});

afterEach(() => {
    window.onload = null;
});

describe('application appearance lifecycle', () => {
    it('releases the theme controller once during beforeunload', async () => {
        await import('./main');

        expect(themeLifecycle.initialize).toHaveBeenCalledOnce();

        window.dispatchEvent(new Event('beforeunload'));
        window.dispatchEvent(new Event('beforeunload'));

        expect(themeLifecycle.disconnect).toHaveBeenCalledOnce();
    });
});
