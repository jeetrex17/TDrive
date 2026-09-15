// Keeps an auth page clear of the software keyboard on phones.
//
// The webview hosts disagree on what a keyboard does to the page: iOS lets it
// cover the bottom and only shrinks visualViewport, Android usually resizes
// the whole viewport. Measuring the strip of the layout viewport the visual
// one no longer covers handles both, because that strip is zero once a host
// has resized the page itself. The page reads the result as a CSS variable
// and gives the strip back to the keyboard, so the primary button stays just
// above it. Without visualViewport the page simply keeps its full height.

import type { Action } from 'svelte/action';
import { isMobilePlatform } from '../../api';

export const KEYBOARD_INSET_PROPERTY = '--auth-keyboard-inset';

export const keyboardInset: Action<HTMLElement> = (node) => {
    const viewport = typeof window === 'undefined' ? null : window.visualViewport;
    if (!viewport || !isMobilePlatform()) return;

    const apply = () => {
        const inset = Math.max(0, Math.round(window.innerHeight - viewport.height - viewport.offsetTop));
        node.style.setProperty(KEYBOARD_INSET_PROPERTY, `${inset}px`);
        // The field being typed into must not end up under the keyboard once
        // the page shrinks around it.
        const active = document.activeElement;
        if (inset > 0 && active instanceof HTMLElement && node.contains(active)) {
            active.scrollIntoView({ block: 'nearest' });
        }
    };

    viewport.addEventListener('resize', apply);
    viewport.addEventListener('scroll', apply);
    apply();

    return {
        destroy() {
            viewport.removeEventListener('resize', apply);
            viewport.removeEventListener('scroll', apply);
        },
    };
};
