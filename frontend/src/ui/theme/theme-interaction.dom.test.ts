import { afterEach, describe, expect, it, vi } from 'vitest';
import { THEME_TRANSITION_CLASS } from './theme-controller';
import { recoverThemeTransitionClick } from './theme-interaction';

function rect(left: number, top: number, width: number, height: number): DOMRect {
    return {
        x: left,
        y: top,
        left,
        top,
        right: left + width,
        bottom: top + height,
        width,
        height,
        toJSON: () => ({}),
    };
}

function overlayClick(x: number, y: number): MouseEvent {
    const event = new MouseEvent('click', {
        bubbles: true,
        cancelable: true,
        clientX: x,
        clientY: y,
    });
    Object.defineProperty(event, 'target', { value: document.documentElement });
    return event;
}

afterEach(() => {
    document.documentElement.classList.remove(THEME_TRANSITION_CLASS);
    document.body.replaceChildren();
});

describe('theme transition click recovery', () => {
    it('replays a transition-overlay click onto the appearance control below it', () => {
        const panel = document.createElement('div');
        const theme = document.createElement('button');
        theme.dataset.themeHitTarget = '';
        panel.appendChild(theme);
        document.body.appendChild(panel);

        vi.spyOn(panel, 'getBoundingClientRect').mockReturnValue(rect(10, 10, 200, 200));
        vi.spyOn(theme, 'getBoundingClientRect').mockReturnValue(rect(25, 30, 80, 60));
        const received = vi.fn();
        theme.addEventListener('click', received);
        document.documentElement.classList.add(THEME_TRANSITION_CLASS);
        const event = overlayClick(50, 55);

        expect(recoverThemeTransitionClick(event, panel)).toBe(true);
        expect(event.defaultPrevented).toBe(true);
        expect(received).toHaveBeenCalledOnce();
        expect(received.mock.calls[0][0]).toMatchObject({ clientX: 50, clientY: 55 });
        expect(document.activeElement).toBe(theme);
    });

    it('does not consume clicks outside the panel or without an active transition', () => {
        const panel = document.createElement('div');
        document.body.appendChild(panel);
        vi.spyOn(panel, 'getBoundingClientRect').mockReturnValue(rect(10, 10, 100, 100));

        expect(recoverThemeTransitionClick(overlayClick(50, 50), panel)).toBe(false);

        document.documentElement.classList.add(THEME_TRANSITION_CLASS);
        expect(recoverThemeTransitionClick(overlayClick(150, 150), panel)).toBe(false);
        expect(recoverThemeTransitionClick(overlayClick(50, 50), null)).toBe(false);
    });
});
