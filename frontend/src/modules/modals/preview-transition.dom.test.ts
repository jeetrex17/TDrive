import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { capturePreviewTransitionSource, createPreviewTransitionController } from './preview-transition';

type MediaChangeListener = (event: MediaQueryListEvent) => void;

let mediaMatches = false;
let mediaChangeListener: MediaChangeListener | null = null;
let originalAnimate: PropertyDescriptor | undefined;
let animationCancels: Mock[] = [];
let animateMock: Mock;

function rect(left: number, top: number, width: number, height: number): DOMRect {
    return {
        x: left,
        y: top,
        left,
        top,
        width,
        height,
        right: left + width,
        bottom: top + height,
        toJSON: () => ({}),
    } as DOMRect;
}

function createSource(): HTMLElement {
    const cell = document.createElement('button');
    cell.className = 'gallery-cell';
    cell.style.borderRadius = '12px';
    cell.innerHTML = '<img class="gallery-thumb" src="data:image/png;base64,source">';
    Object.defineProperty(cell, 'getBoundingClientRect', {
        value: () => rect(20, 40, 120, 120),
    });
    document.body.appendChild(cell);
    return cell;
}

beforeEach(() => {
    document.body.replaceChildren();
    document.documentElement.style.setProperty('--motion-slow', '240ms');
    document.documentElement.style.setProperty('--ease-enter', 'cubic-bezier(0.16, 1, 0.3, 1)');
    mediaMatches = false;
    mediaChangeListener = null;
    animationCancels = [];

    vi.stubGlobal('matchMedia', vi.fn(() => ({
        get matches() {
            return mediaMatches;
        },
        media: '(prefers-reduced-motion: reduce)',
        addEventListener: (_type: string, listener: MediaChangeListener) => {
            mediaChangeListener = listener;
        },
    } as unknown as MediaQueryList)));

    originalAnimate = Object.getOwnPropertyDescriptor(Element.prototype, 'animate');
    animateMock = vi.fn(() => {
        const cancel = vi.fn();
        animationCancels.push(cancel);
        return {
            cancel,
            finished: new Promise<Animation>(() => undefined),
        } as unknown as Animation;
    });
    Object.defineProperty(Element.prototype, 'animate', {
        configurable: true,
        writable: true,
        value: animateMock,
    });
});

afterEach(() => {
    if (originalAnimate) Object.defineProperty(Element.prototype, 'animate', originalAnimate);
    else Reflect.deleteProperty(Element.prototype, 'animate');
    document.documentElement.removeAttribute('style');
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
});

describe('preview shared transition', () => {
    it('moves a pointer-inert visual copy from the gallery cell and cleans it up when interrupted', () => {
        const source = capturePreviewTransitionSource(createSource());
        const overlay = document.createElement('div');
        const target = document.createElement('img');
        target.src = 'data:image/png;base64,target';
        Object.defineProperties(target, {
            complete: { configurable: true, value: true },
            naturalWidth: { configurable: true, value: 1600 },
            getBoundingClientRect: { value: () => rect(200, 100, 800, 600) },
        });
        overlay.appendChild(target);
        document.body.appendChild(overlay);

        const controller = createPreviewTransitionController();
        expect(controller.beginOpen(source, overlay)).toBe(true);
        expect(overlay.classList.contains('is-shared-entering')).toBe(true);
        expect(document.querySelector('.preview-shared-transition-image')?.getAttribute('aria-hidden')).toBe('true');

        expect(controller.finishOpen(target)).toBe(true);
        expect(animateMock).toHaveBeenCalledTimes(2);
        expect(animateMock.mock.calls[0][1]).toMatchObject({ duration: 240, fill: 'both' });
        expect(String(animateMock.mock.calls[0][0][1].transform)).toContain('translate3d(180px, 60px, 0)');

        controller.cancel();
        expect(document.querySelector('.preview-shared-transition-image')).toBeNull();
        expect(overlay.classList.contains('is-shared-entering')).toBe(false);
        expect(target.style.opacity).toBe('');

        expect(controller.playClose(source, target)).toBe(true);
        expect(document.querySelector('.preview-shared-transition-image')).not.toBeNull();
        expect(animateMock).toHaveBeenCalledTimes(3);
        expect(animateMock.mock.calls[2][1]).toMatchObject({ duration: 180, fill: 'both' });
        controller.cancel();
        expect(document.querySelector('.preview-shared-transition-image')).toBeNull();
        expect(animationCancels.every((cancel) => cancel.mock.calls.length === 1)).toBe(true);
    });

    it('skips movement when reduced motion is active and cancels if the preference changes', () => {
        const source = capturePreviewTransitionSource(createSource());
        const overlay = document.createElement('div');
        document.body.appendChild(overlay);

        mediaMatches = true;
        const reducedController = createPreviewTransitionController();
        expect(reducedController.beginOpen(source, overlay)).toBe(false);
        expect(document.querySelector('.preview-shared-transition-image')).toBeNull();
        expect(animateMock).not.toHaveBeenCalled();

        mediaMatches = false;
        const controller = createPreviewTransitionController();
        expect(controller.beginOpen(source, overlay)).toBe(true);
        expect(document.querySelector('.preview-shared-transition-image')).not.toBeNull();

        mediaMatches = true;
        mediaChangeListener?.({ matches: true } as MediaQueryListEvent);
        expect(document.querySelector('.preview-shared-transition-image')).toBeNull();
        expect(overlay.classList.contains('is-shared-entering')).toBe(false);
    });
});
