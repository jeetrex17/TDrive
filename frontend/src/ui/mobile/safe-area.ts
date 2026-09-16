// Keeps the shell clear of the status bar, the gesture handle and the notch.
//
// This exists for Android only. Measured on API 35, its WebView fills
// env(safe-area-inset-top) but leaves the bottom at zero, so the gesture area
// would sit on top of the tab bar. The backend reads the real window insets and
// publishes them here, and CSS takes whichever source is larger.
//
// All four edges, because a turned phone moves the reserved space to the sides:
// the notch takes one and the navigation bar the other, and env() answers zero
// for both on this WebView.
//
// iOS is deliberately left alone. WKWebView already lays its content out inside
// the safe area, so env() there is measured against a viewport that has already
// had the Dynamic Island and the home indicator taken off it. Publishing the
// window insets as well reserved the same space a second time, which showed up
// as a band of dead space above the drive title and under the tab bar.

import { getSafeAreaInsets, isAndroidPlatform } from '../../api';

const VARIABLES = [
    '--mobile-inset-top',
    '--mobile-inset-bottom',
    '--mobile-inset-left',
    '--mobile-inset-right',
] as const;

/**
 * Which reading is current. A refresh is asynchronous, so one fired by a
 * rotation can answer after the rotation that followed it, or after the shell
 * has gone -- in both cases describing a window that no longer exists, and in
 * the second leaving the space reserved forever.
 */
let generation = 0;

function clear(): void {
    const root = document.documentElement.style;
    for (const name of VARIABLES) root.removeProperty(name);
}

async function apply(token: number): Promise<void> {
    const insets = await getSafeAreaInsets();
    if (token !== generation) return;
    // Android answers in device pixels, so the ratio converts them to the CSS
    // pixels the rest of the layout is written in.
    const scale = window.devicePixelRatio || 1;
    const root = document.documentElement.style;
    root.setProperty(VARIABLES[0], `${insets.top / scale}px`);
    root.setProperty(VARIABLES[1], `${insets.bottom / scale}px`);
    root.setProperty(VARIABLES[2], `${insets.left / scale}px`);
    root.setProperty(VARIABLES[3], `${insets.right / scale}px`);
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
        generation += 1;
        clear();
        return () => {};
    }

    const refresh = (): void => {
        const token = ++generation;
        void apply(token).catch((cause) => console.warn('safe-area insets unavailable:', cause));
    };
    refresh();
    window.addEventListener('resize', refresh);
    window.addEventListener('orientationchange', refresh);

    return () => {
        // Anything still in flight is answering for a shell that has gone.
        generation += 1;
        window.removeEventListener('resize', refresh);
        window.removeEventListener('orientationchange', refresh);
        clear();
    };
}
