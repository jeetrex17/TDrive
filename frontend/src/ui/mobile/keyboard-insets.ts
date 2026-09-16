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
 *   Android resizes the WebView instead (adjustResize), which visualViewport
 *   reports inconsistently across versions, so the host's own IME insets are
 *   the dependable source and arrive as "common:keyboard".
 * Both feed the same variable, and the larger wins, so a platform that answers
 * twice cannot double-count and one that answers once still works.
 */

import { onRuntimeEvent, setKeyboardWatch } from '../../api';
import { keyboardOpen } from './mobile-shell-store';

const KEYBOARD = '--mobile-keyboard-inset';

/** Below this, the difference is browser chrome rounding, not a keyboard. */
const MIN_KEYBOARD_PX = 80;

let fromViewport = 0;
let fromHost = 0;
/**
 * The tallest layout viewport seen, which is what the window measures with no
 * keyboard in it. Whatever it has lost since is space the layout has already
 * given up, and must not be reserved a second time.
 */
let fullHeight = 0;

function publish(): void {
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
 * Brings the focused field back above the keyboard. The browser does this
 * itself when it resizes the viewport, but not when it pans one, so iOS needs
 * the nudge and Android is already where it should be -- running it twice is
 * harmless because scrollIntoView on an element already in view does nothing.
 *
 * `nearest` rather than `center`: pulling a field to the middle of the screen
 * scrolls away the label above it, which is the context the user needs to know
 * what they are typing.
 */
function revealFocused(): void {
    const active = document.activeElement;
    if (!(active instanceof HTMLElement)) return;
    if (!active.matches('input, textarea, [contenteditable="true"]')) return;
    active.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
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
        if (!viewport) return;
        fromViewport = measureViewport(viewport);
        publish();
    };

    const stopHostEvents = onRuntimeEvent('common:keyboard', (payload) => {
        fromHost = hostKeyboardHeight(payload);
        publish();
        if (fromHost > 0) revealFocused();
    });

    // Ask the host to report; harmless where visualViewport already answers,
    // and the only source on the platform where it does not.
    setKeyboardWatch(true);

    viewport?.addEventListener('resize', onViewport);
    viewport?.addEventListener('scroll', onViewport);
    window.addEventListener('focusin', revealFocused);
    onViewport();

    return () => {
        setKeyboardWatch(false);
        stopHostEvents();
        viewport?.removeEventListener('resize', onViewport);
        viewport?.removeEventListener('scroll', onViewport);
        window.removeEventListener('focusin', revealFocused);
        fromViewport = 0;
        fromHost = 0;
        fullHeight = 0;
        keyboardOpen.set(false);
        document.documentElement.style.removeProperty(KEYBOARD);
    };
}
