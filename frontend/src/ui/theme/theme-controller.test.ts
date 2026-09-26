import { get } from 'svelte/store';
import { describe, expect, it } from 'vitest';
import { createThemeController, isLinuxWebKit, viewTransitionUnreliable } from './theme-controller';

describe('theme controller without a browser runtime', () => {
    it('preserves the explicit dark default without a browser document', () => {
        const controller = createThemeController({
            storage: { getItem: () => null, setItem: () => {} },
        });

        controller.start();

        expect(get(controller.state).preference.mode).toBe('dark');
        expect(get(controller.state).resolvedAppearance).toBe('dark');
        expect(get(controller.state).resolvedThemeId).toBe('quiet-relay');
        controller.destroy();
    });

    it('remains usable during SSR and applies preferences without touching the DOM', () => {
        const controller = createThemeController();

        expect(() => controller.start()).not.toThrow();
        expect(() => controller.setMode('dark')).not.toThrow();
        expect(get(controller.state).preference.mode).toBe('dark');
        expect(get(controller.state).resolvedThemeId).toBe('quiet-relay');

        expect(() => controller.destroy()).not.toThrow();
    });
});

describe('isLinuxWebKit', () => {
    const webkitGtk = 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15';

    it.each([
        ['WebKitGTK (Wails on Linux)', webkitGtk, true],
        ['WebKitGTK with the Wails application tag', `${webkitGtk} wails.io/`, true],
        ['WPE on aarch64', 'Mozilla/5.0 (X11; Linux aarch64) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15', true],
        ['Chromium on Linux', 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36', false],
        ['Headless Chromium on Linux', 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/128.0.0.0 Safari/537.36', false],
        ['WKWebView on macOS', 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko)', false],
        ['Safari on macOS', 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15', false],
        ['WebView2 on Windows', 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36 Edg/128.0.0.0', false],
        ['Firefox on Linux', 'Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0', false],
        ['happy-dom on Linux CI', 'Mozilla/5.0 (X11; Linux x64) AppleWebKit/537.36 (KHTML, like Gecko) HappyDOM/20.10.6', false],
        ['jsdom on Linux CI', 'Mozilla/5.0 (linux) AppleWebKit/537.36 (KHTML, like Gecko) jsdom/24.0.0', false],
        ['empty', '', false],
        ['undefined', undefined, false],
    ])('%s', (_name, userAgent, expected) => {
        expect(isLinuxWebKit(userAgent)).toBe(expected);
    });
});

describe('viewTransitionUnreliable', () => {
    // Real user agents, so a change in how we sniff is caught by the strings
    // the platforms actually send rather than by ones we invented.
    const IOS_WEBVIEW = 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148';
    const IPAD = 'Mozilla/5.0 (iPad; CPU OS 18_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.2 Safari/605.1.15';
    const LINUX_WEBKITGTK = 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15';
    const MAC_WEBKIT = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.2 Safari/605.1.15';
    const WINDOWS_WEBVIEW2 = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0';
    const ANDROID_WEBVIEW = 'Mozilla/5.0 (Linux; Android 15; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/131.0.0.0 Mobile Safari/537.36';

    it('refuses a root view transition in embedded WebKit', () => {
        expect(viewTransitionUnreliable(IOS_WEBVIEW)).toBe(true);
        expect(viewTransitionUnreliable(IPAD)).toBe(true);
        expect(viewTransitionUnreliable(LINUX_WEBKITGTK)).toBe(true);
    });

    it('allows it everywhere the transition is known to settle', () => {
        // macOS runs the same engine but a full WebKit, not the cut-down
        // embedded one, and the desktop shells are Chromium.
        expect(viewTransitionUnreliable(MAC_WEBKIT)).toBe(false);
        expect(viewTransitionUnreliable(WINDOWS_WEBVIEW2)).toBe(false);
        expect(viewTransitionUnreliable(ANDROID_WEBVIEW)).toBe(false);
        expect(viewTransitionUnreliable(undefined)).toBe(false);
    });

    it('does not mistake Android for Linux WebKitGTK', () => {
        // Android's user agent also says Linux, but it is Chromium, so the
        // Linux guard must not catch it.
        expect(isLinuxWebKit(ANDROID_WEBVIEW)).toBe(false);
    });
});
