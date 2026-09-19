import { afterEach, describe, expect, it, vi } from 'vitest';
import { escapeHTML, handleMenuKeydown, menuItemMarkup } from './menu-keyboard';

afterEach(() => {
    document.body.replaceChildren();
});

function menu(labels: string[]): HTMLButtonElement[] {
    const host = document.createElement('div');
    host.innerHTML = labels.map((label, index) => menuItemMarkup(`data-track="${index}"`, label)).join('');
    document.body.append(host);
    return Array.from(host.querySelectorAll('button'));
}

function press(key: string, target: HTMLElement, buttons: HTMLButtonElement[], close = () => {}) {
    const event = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true });
    Object.defineProperty(event, 'target', { value: target });
    handleMenuKeydown(event, buttons, close);
    return event;
}

describe('menu item markup', () => {
    it('escapes a track title, so a container that names a track with markup cannot break the menu', () => {
        expect(escapeHTML('<b>English</b>')).toBe('&#60;b&#62;English&#60;/b&#62;');
        expect(menuItemMarkup('data-track="1"', 'a & "b"')).not.toContain('"b"');
    });

    it('renders each item as an unselected radio the menu can drive', () => {
        const buttons = menu(['English', 'Japanese']);
        expect(buttons).toHaveLength(2);
        expect(buttons[0].getAttribute('role')).toBe('radio');
        expect(buttons[0].getAttribute('aria-checked')).toBe('false');
    });
});

describe('menu keyboard navigation', () => {
    it('moves focus forward with ArrowDown and ArrowRight alike', () => {
        const buttons = menu(['one', 'two', 'three']);
        buttons[0].focus();
        press('ArrowDown', buttons[0], buttons);
        expect(document.activeElement).toBe(buttons[1]);
        press('ArrowRight', buttons[1], buttons);
        expect(document.activeElement).toBe(buttons[2]);
    });

    it('wraps around both ends rather than stranding focus at the edge', () => {
        const buttons = menu(['one', 'two', 'three']);
        buttons[2].focus();
        press('ArrowDown', buttons[2], buttons);
        expect(document.activeElement).toBe(buttons[0]);
        press('ArrowUp', buttons[0], buttons);
        expect(document.activeElement).toBe(buttons[2]);
    });

    it('jumps to the ends with Home and End', () => {
        const buttons = menu(['one', 'two', 'three']);
        buttons[1].focus();
        press('End', buttons[1], buttons);
        expect(document.activeElement).toBe(buttons[2]);
        press('Home', buttons[2], buttons);
        expect(document.activeElement).toBe(buttons[0]);
    });

    it('swallows the arrow keys, so navigating a menu never also seeks the video behind it', () => {
        const buttons = menu(['one', 'two']);
        buttons[0].focus();
        const event = press('ArrowRight', buttons[0], buttons);
        expect(event.defaultPrevented).toBe(true);
    });

    it('leaves an unhandled key alone, so typing into the custom speed field still works', () => {
        const buttons = menu(['one', 'two']);
        buttons[0].focus();
        const event = press('1', buttons[0], buttons);
        expect(event.defaultPrevented).toBe(false);
    });

    it('closes the menu on Escape', () => {
        const buttons = menu(['one', 'two']);
        const close = vi.fn();
        buttons[0].focus();
        press('Escape', buttons[0], buttons, close);
        expect(close).toHaveBeenCalledTimes(1);
    });

    it('activates the focused item on Enter and Space', () => {
        const buttons = menu(['one', 'two']);
        const clicked = vi.fn();
        buttons[1].addEventListener('click', clicked);
        buttons[1].focus();
        press('Enter', buttons[1], buttons);
        press(' ', buttons[1], buttons);
        expect(clicked).toHaveBeenCalledTimes(2);
    });

    it('steps out of a text field into the list on ArrowDown', () => {
        const buttons = menu(['one', 'two']);
        const input = document.createElement('input');
        document.body.append(input);
        input.focus();
        press('ArrowDown', input, buttons);
        expect(document.activeElement).toBe(buttons[0]);
    });

    it('treats focus outside the list as if it sat on the first item', () => {
        const buttons = menu(['one', 'two', 'three']);
        document.body.focus();
        press('ArrowDown', document.body, buttons);
        expect(document.activeElement).toBe(buttons[1]);
    });
});
