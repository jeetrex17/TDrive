// One owner per edge: the keyboard publishes its own variable and tokens.css
// folds it into --inset-bottom with max(), so nothing double-counts.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const hostListeners = new Map<string, (payload: unknown) => void>();
const setKeyboardWatch = vi.hoisted(() => vi.fn());

vi.mock('../../api', () => ({
    setKeyboardWatch,
    onRuntimeEvent: (name: string, cb: (payload: unknown) => void) => {
        hostListeners.set(name, cb);
        return () => hostListeners.delete(name);
    },
}));

import { activateKeyboardInsets, hostKeyboardHeight } from './keyboard-insets';

function inset(): string {
    return document.documentElement.style.getPropertyValue('--mobile-keyboard-inset');
}

function emitHostKeyboard(payload: unknown): void {
    hostListeners.get('common:keyboard')?.(payload);
}

/** Stands in for the visual viewport iOS shrinks when the keyboard opens. */
function fakeViewport(height: number, offsetTop = 0) {
    const listeners = new Map<string, () => void>();
    return {
        height,
        offsetTop,
        addEventListener: (type: string, cb: () => void) => listeners.set(type, cb),
        removeEventListener: (type: string) => listeners.delete(type),
        emit: () => listeners.get('resize')?.(),
        set(next: number) { (this as { height: number }).height = next; },
    };
}

let dispose: () => void;

beforeEach(() => {
    hostListeners.clear();
    setKeyboardWatch.mockClear();
    document.documentElement.style.removeProperty('--mobile-keyboard-inset');
    window.innerHeight = 800;
});

afterEach(() => {
    dispose?.();
    Reflect.deleteProperty(window, 'visualViewport');
});

describe('activateKeyboardInsets', () => {
    it('asks the host to report and stops asking on teardown', () => {
        dispose = activateKeyboardInsets();
        expect(setKeyboardWatch).toHaveBeenCalledWith(true);
        dispose();
        expect(setKeyboardWatch).toHaveBeenLastCalledWith(false);
    });

    it('measures the keyboard from the shrunken visual viewport', () => {
        const viewport = fakeViewport(800);
        Object.defineProperty(window, 'visualViewport', { value: viewport, configurable: true });
        dispose = activateKeyboardInsets();
        expect(inset()).toBe('0px');

        viewport.set(480);
        viewport.emit();
        expect(inset()).toBe('320px');
    });

    it('takes the host height where the viewport reports nothing', () => {
        dispose = activateKeyboardInsets();
        emitHostKeyboard({ visible: true, height: 300 });
        expect(inset()).toBe('300px');
    });

    it('takes the larger source rather than their sum, so nothing double-counts', () => {
        const viewport = fakeViewport(500);
        Object.defineProperty(window, 'visualViewport', { value: viewport, configurable: true });
        dispose = activateKeyboardInsets();
        // Viewport says 300; the host says 290 for the same keyboard. Adding
        // them would reserve 590px of a 800px screen for a 300px keyboard.
        emitHostKeyboard({ visible: true, height: 290 });
        expect(inset()).toBe('300px');
    });

    it('ignores movement too small to be a keyboard', () => {
        const viewport = fakeViewport(800);
        Object.defineProperty(window, 'visualViewport', { value: viewport, configurable: true });
        dispose = activateKeyboardInsets();
        viewport.set(760); // browser chrome, not keys
        viewport.emit();
        expect(inset()).toBe('0px');
    });

    it('clears the reservation when the keyboard closes', () => {
        dispose = activateKeyboardInsets();
        emitHostKeyboard({ visible: true, height: 300 });
        expect(inset()).toBe('300px');
        emitHostKeyboard({ visible: false, height: 0 });
        expect(inset()).toBe('0px');
    });

    it('leaves nothing reserved after teardown', () => {
        dispose = activateKeyboardInsets();
        emitHostKeyboard({ visible: true, height: 300 });
        dispose();
        expect(inset()).toBe('');
    });
});

describe('hostKeyboardHeight', () => {
    it('reads a visible keyboard and refuses everything else', () => {
        expect(hostKeyboardHeight({ visible: true, height: 320 })).toBe(320);
        expect(hostKeyboardHeight({ height: 320 })).toBe(320);
        expect(hostKeyboardHeight({ visible: false, height: 320 })).toBe(0);
        expect(hostKeyboardHeight({ visible: true, height: 'tall' })).toBe(0);
        expect(hostKeyboardHeight(null)).toBe(0);
        expect(hostKeyboardHeight('320')).toBe(0);
    });
});
