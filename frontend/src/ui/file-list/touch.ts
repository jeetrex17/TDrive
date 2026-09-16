// Touch interactions shared by the phone lists: the haptic tick, a delegated
// long press and pull to refresh. Callers bind these only on iOS and Android,
// so the desktop never installs a listener it cannot use.

import { mount, unmount } from 'svelte';
import LoaderCircleIcon from '@lucide/svelte/icons/loader-circle';
import { hapticPress } from '../mobile/haptics';
import { claimGesture, type SwipeClaim } from './swipe-actions';

const LONG_PRESS_MS = 350;
const LONG_PRESS_SLOP_PX = 10;
// A click the browser synthesises for a press that already opened its menu
// arrives within this window of the press firing.
const LONG_PRESS_CLICK_GUARD_MS = 700;
const PULL_THRESHOLD_PX = 64;
const PULL_MAX_PX = 96;
const PULL_MIN_SPIN_MS = 500;

/**
 * Kept as the name the list code already calls. The behaviour now lives in
 * ui/mobile/haptics, which both phone platforms reach through one binding.
 */
export const lightHaptic = hapticPress;

export type LongPressHandler = (item: HTMLElement, clientX: number, clientY: number, origin: Element | null) => void;

/**
 * Opens the handler for a 350 ms press on any descendant matching `selector`.
 * Moving past the slop or lifting first cancels it, and the click the browser
 * synthesises on release is swallowed so the press never also opens the row.
 * The native context menu is suppressed on the host because the press already
 * stands in for it.
 */
export function bindLongPress(host: HTMLElement, selector: string, onLongPress: LongPressHandler): () => void {
    let timer = 0;
    let item: HTMLElement | null = null;
    let pointerId = -1;
    let startX = 0;
    let startY = 0;
    let firedAt = 0;

    const cancel = () => {
        window.clearTimeout(timer);
        timer = 0;
        item = null;
        pointerId = -1;
    };
    const onPointerDown = (event: PointerEvent) => {
        if (event.pointerType === 'mouse' || !event.isPrimary) return;
        const origin = event.target instanceof Element ? event.target : null;
        const target = origin?.closest<HTMLElement>(selector);
        if (!target) return;
        cancel();
        item = target;
        pointerId = event.pointerId;
        startX = event.clientX;
        startY = event.clientY;
        timer = window.setTimeout(() => {
            const pressed = item;
            cancel();
            if (!pressed?.isConnected) return;
            firedAt = Date.now();
            lightHaptic();
            onLongPress(pressed, startX, startY, origin);
        }, LONG_PRESS_MS);
    };
    const onPointerMove = (event: PointerEvent) => {
        if (event.pointerId !== pointerId) return;
        if (Math.hypot(event.clientX - startX, event.clientY - startY) > LONG_PRESS_SLOP_PX) cancel();
    };
    const onPointerEnd = (event: PointerEvent) => {
        if (event.pointerId === pointerId) cancel();
    };
    const onClick = (event: MouseEvent) => {
        if (!firedAt || Date.now() - firedAt > LONG_PRESS_CLICK_GUARD_MS) return;
        firedAt = 0;
        event.preventDefault();
        event.stopPropagation();
    };
    const onContextMenu = (event: MouseEvent) => {
        if (!event.isTrusted) return;
        // Only on the rows, which have a menu of their own for the press to
        // stand in for. Swallowed across the whole host it also took the
        // platform's own selection and lookup off the group headings and the
        // empty space around the list, where nothing replaces them.
        const origin = event.target instanceof Element ? event.target : null;
        if (!origin?.closest(selector)) return;
        event.preventDefault();
        event.stopPropagation();
    };
    // WebKit only paints :active for a touch when something listens to touchstart.
    const onTouchStart = () => undefined;

    host.addEventListener('pointerdown', onPointerDown);
    host.addEventListener('pointermove', onPointerMove);
    host.addEventListener('pointerup', onPointerEnd);
    host.addEventListener('pointercancel', onPointerEnd);
    host.addEventListener('click', onClick, true);
    host.addEventListener('contextmenu', onContextMenu, true);
    host.addEventListener('touchstart', onTouchStart, { passive: true });
    return () => {
        cancel();
        host.removeEventListener('pointerdown', onPointerDown);
        host.removeEventListener('pointermove', onPointerMove);
        host.removeEventListener('pointerup', onPointerEnd);
        host.removeEventListener('pointercancel', onPointerEnd);
        host.removeEventListener('click', onClick, true);
        host.removeEventListener('contextmenu', onContextMenu, true);
        host.removeEventListener('touchstart', onTouchStart);
    };
}

/**
 * Runs `refresh` when the list is dragged down from its top by more than the
 * threshold. A small ring slides in over the first rows as the pull grows,
 * ticks once when armed and spins until the refresh settles; the rows stay
 * put so a virtualised list is never transformed.
 */
export function bindPullToRefresh(host: HTMLElement, refresh: () => unknown): () => void {
    const indicator = document.createElement('div');
    indicator.className = 'pull-refresh';
    indicator.setAttribute('aria-hidden', 'true');
    const disc = document.createElement('span');
    disc.className = 'pull-refresh-disc';
    // Lucide's loader arc is the ring; the pull turns it, the refresh spins it.
    const ring = mount(LoaderCircleIcon, { target: disc, props: { size: 18, strokeWidth: 2.25 } });
    indicator.append(disc);
    host.prepend(indicator);

    let startY = -1;
    let startX = 0;
    // The row swipe and the pull compete for the same first pixels, so they
    // settle it with one verdict instead of each acting on its own reading.
    let claim: SwipeClaim = 'undecided';
    let pull = 0;
    let armed = false;
    // Armed says what the ring is showing; buzzed says the gesture has already
    // had its one tick. A thumb resting on the threshold crosses it over and
    // over, and a buzz per crossing is what teaches people to turn haptics off.
    let buzzed = false;
    let busy = false;

    const paint = () => {
        indicator.style.setProperty('--pull', `${pull}px`);
        indicator.style.setProperty('--pull-progress', String(Math.min(1, pull / PULL_THRESHOLD_PX)));
        indicator.classList.toggle('is-pulling', pull > 0);
        indicator.classList.toggle('is-armed', armed);
    };
    const settle = () => {
        startY = -1;
        pull = 0;
        armed = false;
        paint();
    };
    const onTouchStart = (event: TouchEvent) => {
        if (busy || host.scrollTop > 0 || event.touches.length !== 1) return;
        startY = event.touches[0].clientY;
        startX = event.touches[0].clientX;
        claim = 'undecided';
        buzzed = false;
    };
    const onTouchMove = (event: TouchEvent) => {
        if (startY < 0 || busy) return;
        const dy = event.touches[0].clientY - startY;
        const dx = event.touches[0].clientX - startX;
        if (dy <= 0 || host.scrollTop > 0) {
            if (pull > 0) settle();
            return;
        }
        if (event.cancelable) event.preventDefault();
        // Nothing is drawn until the movement is legibly vertical: a drag down
        // and to the left used to open a row and slide the refresh ring in
        // behind it at the same time.
        if (claim === 'undecided') claim = claimGesture(dx, dy, startX);
        if (claim === 'row') {
            if (pull > 0) settle();
            return;
        }
        pull = Math.min(PULL_MAX_PX, dy * 0.5);
        armed = pull >= PULL_THRESHOLD_PX;
        if (armed && !buzzed) {
            buzzed = true;
            lightHaptic();
        }
        paint();
    };
    const onTouchEnd = () => {
        if (startY < 0) return;
        if (!armed) {
            settle();
            return;
        }
        busy = true;
        startY = -1;
        armed = false;
        pull = PULL_THRESHOLD_PX;
        paint();
        indicator.classList.add('is-refreshing');
        const minimumSpin = new Promise((resolve) => window.setTimeout(resolve, PULL_MIN_SPIN_MS));
        Promise.all([Promise.resolve().then(refresh).catch(() => undefined), minimumSpin]).then(() => {
            busy = false;
            indicator.classList.remove('is-refreshing');
            settle();
        });
    };

    host.addEventListener('touchstart', onTouchStart, { passive: true });
    host.addEventListener('touchmove', onTouchMove, { passive: false });
    host.addEventListener('touchend', onTouchEnd);
    host.addEventListener('touchcancel', onTouchEnd);
    return () => {
        host.removeEventListener('touchstart', onTouchStart);
        host.removeEventListener('touchmove', onTouchMove);
        host.removeEventListener('touchend', onTouchEnd);
        host.removeEventListener('touchcancel', onTouchEnd);
        void unmount(ring);
        indicator.remove();
    };
}
