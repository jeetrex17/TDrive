// Keeps the shell clear of the status bar and the gesture handle.
//
// This exists for Android only. Measured on API 35, its WebView fills
// env(safe-area-inset-top) but leaves the bottom at zero, so the gesture area
// would sit on top of the tab bar. The backend reads the real window insets and
// publishes them here, and tokens.css takes whichever source is larger.
//
// iOS is deliberately left alone. WKWebView already lays its content out inside
// the safe area, so env() there is measured against a viewport that has already
// had the Dynamic Island and the home indicator taken off it. Publishing the
// window insets as well reserved the same space a second time, which showed up
// as a band of dead space above the drive title and under the tab bar.

import { getSafeAreaInsets, isAndroidPlatform } from '../../api';

const TOP = '--mobile-inset-top';
const BOTTOM = '--mobile-inset-bottom';

function clear(): void {
    const root = document.documentElement.style;
    root.removeProperty(TOP);
    root.removeProperty(BOTTOM);
}

async function apply(): Promise<void> {
    const insets = await getSafeAreaInsets();
    // Android answers in device pixels, so the ratio converts them to the CSS
    // pixels the rest of the layout is written in.
    const scale = window.devicePixelRatio || 1;
    const root = document.documentElement.style;
    root.setProperty(TOP, `${insets.top / scale}px`);
    root.setProperty(BOTTOM, `${insets.bottom / scale}px`);
}

/**
 * Publishes the insets for as long as the phone shell is mounted, refreshing
 * them when the window changes shape (rotation, split screen, a resized
 * window), which is when the reserved edges move.
 *
 * On any platform whose webview reports its own insets correctly, this does
 * nothing and CSS env() is left to answer alone.
 */
export function activateSafeArea(): () => void {
    if (typeof window === 'undefined') return () => {};
    if (!isAndroidPlatform()) {
        clear();
        return () => {};
    }

    const refresh = (): void => {
        void apply().catch((cause) => console.warn('safe-area insets unavailable:', cause));
    };
    refresh();
    window.addEventListener('resize', refresh);
    window.addEventListener('orientationchange', refresh);

    return () => {
        window.removeEventListener('resize', refresh);
        window.removeEventListener('orientationchange', refresh);
        clear();
    };
}
