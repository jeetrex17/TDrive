// One owner per edge: the keyboard publishes its own variable and tokens.css
// folds it into --inset-bottom with max(), so nothing double-counts.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const hostListeners = new Map<string, (payload: unknown) => void>();
const setKeyboardWatch = vi.hoisted(() => vi.fn());
const isAndroidPlatform = vi.hoisted(() => vi.fn(() => false));

vi.mock('../../api', () => ({
    setKeyboardWatch,
    isAndroidPlatform,
    onRuntimeEvent: (name: string, cb: (payload: unknown) => void) => {
        hostListeners.set(name, cb);
        return () => hostListeners.delete(name);
    },
}));

import { activateKeyboardInsets, hostKeyboardHeight, isPlausibleKeyboard, toCssPixels } from './keyboard-insets';

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
    isAndroidPlatform.mockReturnValue(false);
    window.devicePixelRatio = 1;
    document.documentElement.style.removeProperty('--mobile-keyboard-inset');
    window.innerHeight = 800;
    window.innerWidth = 400;
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

    it('reserves what an Android keyboard covers, not its device-pixel count', () => {
        isAndroidPlatform.mockReturnValue(true);
        window.devicePixelRatio = 2.625;
        window.innerHeight = 915;
        dispose = activateKeyboardInsets();
        // The window keeps its full height on an edge-to-edge activity, so the
        // host event is the only source and its 767 device pixels are the 292
        // CSS pixels the keys actually cover.
        emitHostKeyboard({ visible: true, height: 767 });
        expect(inset()).toBe('292px');
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

    it('waits out the height Android reports before the keys have slid in', () => {
        dispose = activateKeyboardInsets();
        // 94% of the window: the keyboard's window measured from where it
        // starts. Acting on it would collapse the page for the animation.
        emitHostKeyboard({ visible: true, height: 750 });
        expect(inset()).toBe('0px');
        emitHostKeyboard({ visible: true, height: 300 });
        expect(inset()).toBe('300px');
    });

    it('keeps reserving the keyboard after the phone is turned on its side', () => {
        dispose = activateKeyboardInsets();
        emitHostKeyboard({ visible: true, height: 300 });
        expect(inset()).toBe('300px');

        // Landscape: a shorter, wider window. Taken against the portrait height
        // the 388px it "lost" would cancel the keyboard out entirely.
        window.innerWidth = 800;
        window.innerHeight = 412;
        window.dispatchEvent(new Event('resize'));
        emitHostKeyboard({ visible: true, height: 260 });
        expect(inset()).toBe('260px');
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

describe('isPlausibleKeyboard', () => {
    it('takes the tallest real keyboard and refuses the window behind one', () => {
        window.innerHeight = 412;
        expect(isPlausibleKeyboard(261)).toBe(true); // landscape Gboard, 63%
        expect(isPlausibleKeyboard(0)).toBe(true);
        window.innerHeight = 915;
        expect(isPlausibleKeyboard(336)).toBe(true); // portrait QWERTY
        expect(isPlausibleKeyboard(862)).toBe(false); // the pre-animation report
    });
});

describe('toCssPixels', () => {
    it('brings Android device pixels down to the CSS pixels the layout uses', () => {
        isAndroidPlatform.mockReturnValue(true);
        window.devicePixelRatio = 2.625;
        // A real Pixel keyboard: 767 of 2400 device pixels, 292 of 915 CSS ones.
        expect(Math.round(toCssPixels(767))).toBe(292);
        expect(toCssPixels(0)).toBe(0);
    });

    it('leaves iOS points alone, because they already are CSS pixels', () => {
        window.devicePixelRatio = 3;
        expect(toCssPixels(336)).toBe(336);
    });
});
