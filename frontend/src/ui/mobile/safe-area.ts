// Keeps the shell clear of the status bar and the gesture handle.
//
// CSS alone cannot do this on Android: measured on API 35, its WebView fills
// env(safe-area-inset-top) but leaves the bottom at zero, so the gesture area
// would sit on top of the tab bar. The backend reads the real window insets
// instead (Wails reports them on both phone platforms) and they land on :root,
// where tokens.css takes whichever of the two sources is larger.

import { getSafeAreaInsets, isAndroidPlatform } from '../../api';

const TOP = '--mobile-inset-top';
const BOTTOM = '--mobile-inset-bottom';

async function apply(): Promise<void> {
    const insets = await getSafeAreaInsets();
    // Android answers in device pixels and iOS in points, which are already
    // CSS pixels; scaling the wrong platform would double-count the bars.
    const scale = isAndroidPlatform() ? (window.devicePixelRatio || 1) : 1;
    const root = document.documentElement.style;
    root.setProperty(TOP, `${insets.top / scale}px`);
    root.setProperty(BOTTOM, `${insets.bottom / scale}px`);
}

/**
 * Publishes the insets for as long as the phone shell is mounted, refreshing
 * them when the window changes shape (rotation, split screen, a resized
 * window), which is when the reserved edges move.
 */
export function activateSafeArea(): () => void {
    if (typeof window === 'undefined') return () => {};

    const refresh = (): void => {
        void apply().catch((cause) => console.warn('safe-area insets unavailable:', cause));
    };
    refresh();
    window.addEventListener('resize', refresh);
    window.addEventListener('orientationchange', refresh);

    return () => {
        window.removeEventListener('resize', refresh);
        window.removeEventListener('orientationchange', refresh);
        const root = document.documentElement.style;
        root.removeProperty(TOP);
        root.removeProperty(BOTTOM);
    };
}
