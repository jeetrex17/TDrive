// The keyboard action pads a page by the strip of the layout viewport that the
// visual viewport stops covering while a keyboard is up.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { KEYBOARD_INSET_PROPERTY, keyboardInset } from './keyboard-inset';

class FakeViewport extends EventTarget {
    height = 844;
    offsetTop = 0;
}

let viewport: FakeViewport;
let node: HTMLElement;

beforeEach(() => {
    viewport = new FakeViewport();
    Object.defineProperty(window, 'visualViewport', { configurable: true, value: viewport });
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 844 });
    window.history.replaceState(null, '', '/?mobile=1');
    node = document.createElement('form');
    document.body.appendChild(node);
});

afterEach(() => {
    node.remove();
    window.history.replaceState(null, '', '/');
    Reflect.deleteProperty(window, 'visualViewport');
});

describe('keyboardInset', () => {
    it('follows the visual viewport while a keyboard covers the page', () => {
        const action = keyboardInset(node);
        expect(node.style.getPropertyValue(KEYBOARD_INSET_PROPERTY)).toBe('0px');

        viewport.height = 508;
        viewport.dispatchEvent(new Event('resize'));
        expect(node.style.getPropertyValue(KEYBOARD_INSET_PROPERTY)).toBe('336px');

        // iOS pans the visual viewport to reveal a field; the covered strip
        // shrinks by the pan so the bottom of the page still meets the keyboard.
        viewport.offsetTop = 100;
        viewport.dispatchEvent(new Event('scroll'));
        expect(node.style.getPropertyValue(KEYBOARD_INSET_PROPERTY)).toBe('236px');

        action?.destroy?.();
        viewport.height = 300;
        viewport.dispatchEvent(new Event('resize'));
        expect(node.style.getPropertyValue(KEYBOARD_INSET_PROPERTY)).toBe('236px');
    });

    it('reports no inset when the host resized the page itself', () => {
        Object.defineProperty(window, 'innerHeight', { configurable: true, value: 508 });
        viewport.height = 508;
        keyboardInset(node);
        expect(node.style.getPropertyValue(KEYBOARD_INSET_PROPERTY)).toBe('0px');
    });

    it('keeps the focused field in view once the page shrinks', () => {
        const input = document.createElement('input');
        node.appendChild(input);
        input.focus();
        const scrollIntoView = vi.fn();
        input.scrollIntoView = scrollIntoView;

        keyboardInset(node);
        viewport.height = 400;
        viewport.dispatchEvent(new Event('resize'));
        expect(scrollIntoView).toHaveBeenCalledWith({ block: 'nearest' });
    });

    it('stays inert on desktop and without visualViewport', () => {
        window.history.replaceState(null, '', '/');
        expect(keyboardInset(node)).toBeUndefined();
        expect(node.style.getPropertyValue(KEYBOARD_INSET_PROPERTY)).toBe('');

        window.history.replaceState(null, '', '/?mobile=1');
        Reflect.deleteProperty(window, 'visualViewport');
        expect(keyboardInset(node)).toBeUndefined();
    });
});
