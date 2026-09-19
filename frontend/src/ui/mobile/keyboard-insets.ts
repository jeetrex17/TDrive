/**
 * How much of the screen the software keyboard is covering, published as one
 * CSS variable.
 *
 * The rule this file exists to keep is one owner per edge. Three things want to
 * reserve space at the bottom -- the home indicator, the Android gesture bar,
 * and the keyboard -- and if each adds its own padding somewhere the space is
 * counted two or three times and the content floats above a gap. So nothing
 * here adds padding. It publishes `--mobile-keyboard-inset`, and tokens.css
 * folds it into `--inset-bottom` with `max()`, which is also correct rather
 * than merely convenient: an open keyboard already covers the gesture area, so
 * the larger of the two is the real reserved height, never their sum.
 *
 * Two sources, because neither platform reports it the same way:
 *   iOS pans the layout viewport under a fixed window, so visualViewport is
 *   the only thing that sees the keyboard, and it sees it exactly.
 *   Android hands the keyboard to the app as a window inset. Once the activity
 *   takes the whole window, nothing resizes the WebView, so visualViewport
 *   never notices the keyboard at all and the host's own IME insets, arriving
 *   as "common:keyboard", are the only source there.
 * Both feed the same variable, and the larger wins, so a platform that answers
 * twice cannot double-count and one that answers once still works.
 */

import { isAndroidPlatform, onRuntimeEvent, setKeyboardWatch } from '../../api';
import { keyboardOpen } from './mobile-shell-store';
import { scrollBehavior } from './motion';

const KEYBOARD = '--mobile-keyboard-inset';

/** Below this, the difference is browser chrome rounding, not a keyboard. */
const MIN_KEYBOARD_PX = 80;

/**
 * Above this share of the window, it is not a keyboard either. Android reports
 * the IME inset once from where the keyboard's window starts rather than where
 * it lands -- 94% of a Pixel's screen -- about two thirds of a second before the
 * real height arrives. Taken at its word it collapses the page to a sliver for
 * the length of the animation. A landscape keyboard reaches about 63%, so this
 * leaves room for the tallest real one and refuses the other.
 */
const MAX_KEYBOARD_SHARE = 0.75;

let fromViewport = 0;
let fromHost = 0;
/**
 * The tallest layout viewport seen at the width below, which is what the window
 * measures with no keyboard in it. Whatever it has lost since is space the
 * layout has already given up, and must not be reserved a second time.
 */
let fullHeight = 0;
/**
 * A keyboard never changes how wide the window is, so a width that changes is a
 * different window -- a rotation, a split screen -- and the height remembered
 * for the old one no longer describes it. Kept across a turn to landscape, a
 * portrait height reads the whole difference as space the layout already gave
 * up and leaves the keyboard covering the page with nothing reserved for it.
 */
let fullWidth = 0;

function publish(): void {
    if (window.innerWidth !== fullWidth) {
        fullWidth = window.innerWidth;
        fullHeight = 0;
    }
    if (fromHost === 0 && fromViewport === 0) fullHeight = Math.max(fullHeight, window.innerHeight);

    // A host that resizes its web view for the keyboard (Android's adjustResize)
    // has already taken that space out of the layout, so reserving the height it
    // reports would count the keyboard twice and leave the bar floating a
    // keyboard's height above the keys. Only the part the window still covers is
    // owed padding.
    const shrunkBy = Math.max(0, fullHeight - window.innerHeight);
    const stillCovered = Math.max(0, fromHost - shrunkBy);

    const height = Math.max(fromViewport, stillCovered);
    const root = document.documentElement.style;
    if (height >= MIN_KEYBOARD_PX) root.setProperty(KEYBOARD, `${Math.round(height)}px`);
    else root.setProperty(KEYBOARD, '0px');

    // Whether a keyboard is up is a different question from how much padding it
    // is owed: it is up even when the layout already made room for it.
    keyboardOpen.set(Math.max(fromViewport, fromHost) >= MIN_KEYBOARD_PX);
}

/**
 * The part of the layout viewport the visual viewport no longer covers. On iOS
 * that is the keyboard plus any accessory bar, which is what we want: the
 * accessory bar hides content just as effectively as the keys do.
 */
function measureViewport(viewport: VisualViewport): number {
    return Math.max(0, window.innerHeight - viewport.height - viewport.offsetTop);
}

/** Reads {visible, height} out of the host's keyboard event, defensively. */
export function hostKeyboardHeight(payload: unknown): number {
    if (payload === null || typeof payload !== 'object') return 0;
    const record = payload as Record<string, unknown>;
    if (record.visible === false) return 0;
    const height = Number(record.height);
    return Number.isFinite(height) && height > 0 ? height : 0;
}

/**
 * The hosts do not answer in the same unit. Android measures its window insets
 * in device pixels, the same as the safe area it reports (see safe-area.ts), so
 * a 767px keyboard on a 2.6x screen is 292 CSS pixels; taking it at face value
 * reserves two and a half keyboards and pins the page against the status bar.
 * iOS answers in points, which are already CSS pixels, and is left alone.
 */
export function toCssPixels(hostHeight: number): number {
    if (hostHeight <= 0 || !isAndroidPlatform()) return hostHeight;
    const scale = window.devicePixelRatio || 1;
    return hostHeight / scale;
}

/** Whether a reported height describes a keyboard or the window behind one. */
export function isPlausibleKeyboard(height: number): boolean {
    return height <= window.innerHeight * MAX_KEYBOARD_SHARE;
}

/**
 * Brings the focused field back above the keyboard. The browser does this
 * itself when it resizes the viewport, but not when it pans one, so iOS needs
 * the nudge and Android is already where it should be -- running it twice is
 * harmless because scrollIntoView on an element already in view does nothing.
 *
 * The glide goes under Reduce Motion, where a page that slides on its own while
 * someone is typing is exactly what the setting is asking us not to do.
 *
 * `nearest` rather than `center`: pulling a field to the middle of the screen
 * scrolls away the label above it, which is the context the user needs to know
 * what they are typing. Where the field stops is the page's business, not this
 * module's: a scrolling surface says how close to its edges a field may land
 * with scroll-padding, and a field says how much of what sits above it to bring
 * along with scroll-margin. Both are honoured by the browser's own scroll into
 * view as well as by this one, which is why they live in CSS.
 */
function revealFocused(): void {
    const active = document.activeElement;
    if (!(active instanceof HTMLElement)) return;
    if (!active.matches('input, textarea, [contenteditable="true"]')) return;
    active.scrollIntoView({ block: 'nearest', behavior: scrollBehavior() });
}

/**
 * Publishes the keyboard inset for as long as the phone shell is mounted.
 * Returns the teardown, which also clears the variable so a shell that
 * unmounts mid-edit does not leave the space reserved forever.
 */
export function activateKeyboardInsets(): () => void {
    if (typeof window === 'undefined') return () => {};

    const viewport = window.visualViewport ?? null;

    const onViewport = (): void => {
        if (viewport) fromViewport = measureViewport(viewport);
        publish();
    };

    const stopHostEvents = onRuntimeEvent('common:keyboard', (payload) => {
        const reported = toCssPixels(hostKeyboardHeight(payload));
        // Keeping the last believable answer rather than acting on this one:
        // the real height follows within the animation either way.
        if (!isPlausibleKeyboard(reported)) return;
        fromHost = reported;
        publish();
        if (fromHost > 0) revealFocused();
    });

    // Ask the host to report; harmless where visualViewport already answers,
    // and the only source on the platform where it does not.
    setKeyboardWatch(true);

    viewport?.addEventListener('resize', onViewport);
    viewport?.addEventListener('scroll', onViewport);
    // Rotating is how the window changes shape under a keyboard that is still
    // up, and the host repeats no inset for it, so the reservation is recomputed
    // against the new window here.
    window.addEventListener('resize', onViewport);
    window.addEventListener('orientationchange', onViewport);
    window.addEventListener('focusin', revealFocused);
    onViewport();

    return () => {
        setKeyboardWatch(false);
        stopHostEvents();
        viewport?.removeEventListener('resize', onViewport);
        viewport?.removeEventListener('scroll', onViewport);
        window.removeEventListener('resize', onViewport);
        window.removeEventListener('orientationchange', onViewport);
        window.removeEventListener('focusin', revealFocused);
        fromViewport = 0;
        fromHost = 0;
        fullHeight = 0;
        fullWidth = 0;
        keyboardOpen.set(false);
        document.documentElement.style.removeProperty(KEYBOARD);
    };
}
