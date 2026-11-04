import { afterEach, describe, expect, it, vi } from 'vitest';
import { MobileVideoControls } from './mobile-controls';
import type { MobilePlayerRequest } from '../../api/mobile-player';

const initial = { brightness: 0.5, volume: 0.4, orientation: 'portrait', volumeMode: 'system' };
const controllers: MobileVideoControls[] = [];
function setup(request = vi.fn(async (_request: MobilePlayerRequest): Promise<unknown> => initial)) {
    document.body.innerHTML = '<button id="rotate" hidden></button><button id="volume" hidden></button><div id="feedback" hidden></div><div id="legacy"></div>';
    const dom = {
        rotate: document.querySelector<HTMLButtonElement>('#rotate'),
        volume: document.querySelector<HTMLButtonElement>('#volume'),
        feedback: document.querySelector<HTMLElement>('#feedback'),
    };
    const controller = new MobileVideoControls(dom, request, true);
    controllers.push(controller);
    return { controller, request, dom };
}
afterEach(() => {
    controllers.splice(0).forEach((controller) => controller.destroy());
    document.body.replaceChildren();
    vi.restoreAllMocks();
});

describe('mobile video controls', () => {
    it('maps left and right swipes to bounded native brightness and volume', async () => {
        const { controller, request } = setup();
        controller.open();
        await vi.waitFor(() => expect(request).toHaveBeenCalledTimes(1));
        expect(controller.startDrag(0.2, 200)).toBe(true);
        controller.drag(-200);
        await vi.waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: 'brightness', value: 1 })));
        controller.endDrag();
        expect(controller.startDrag(0.8, 200)).toBe(true);
        controller.drag(200);
        await vi.waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: 'volume', value: 0 })));
        expect(controller.startDrag(0.5, 200)).toBe(false);
    });

    it('starts a volume swipe from the current hardware level', async () => {
        const request = vi.fn(async (request: MobilePlayerRequest) => request.operation === 'state'
            ? { ...initial, volume: 0.8 } : initial);
        const { controller } = setup(request);
        controller.open();
        await vi.waitFor(() => expect(request).toHaveBeenCalledTimes(1));
        controller.startDrag(0.8, 200);
        controller.drag(-20);
        await vi.waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: 'volume', value: 0.9 })));
    });

    it('uses the real iOS volume slider rather than sending programmatic volume changes', async () => {
        const request = vi.fn(async (_request: MobilePlayerRequest) => ({ ...initial, volumeMode: 'native-slider' }));
        const { controller } = setup(request);
        controller.open();
        await vi.waitFor(() => expect(request).toHaveBeenCalledTimes(1));
        expect(controller.startDrag(0.9, 200)).toBe(true);
        controller.drag(-100);
        expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: 'showVolume' }));
        expect(request).not.toHaveBeenCalledWith(expect.objectContaining({ operation: 'volume' }));
    });

    it('coalesces a fast drag while a native request is in flight', async () => {
        let finish: (() => void) | undefined;
        const request = vi.fn(async (request: MobilePlayerRequest) => {
            if (request.operation === 'brightness') await new Promise<void>((resolve) => { finish = resolve; });
            return initial;
        });
        const { controller } = setup(request);
        controller.open();
        await vi.waitFor(() => expect(request).toHaveBeenCalledTimes(1));
        controller.startDrag(0.1, 200);
        controller.drag(-10);
        await vi.waitFor(() => expect(request.mock.calls.filter(([call]) => call.operation === 'brightness')).toHaveLength(1));
        controller.drag(-20);
        controller.drag(-50);
        expect(request.mock.calls.filter(([call]) => call.operation === 'brightness')).toHaveLength(1);
        finish!();
        await vi.waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: 'brightness', value: 0.75 })));
        finish!();
    });

    it('does not restore late begin results after close', async () => {
        let finish: ((value: unknown) => void) | undefined;
        const request = vi.fn((request: MobilePlayerRequest): Promise<unknown> => request.operation === 'begin'
            ? new Promise((resolve) => { finish = resolve; }) : Promise.resolve({}));
        const { controller, dom } = setup(request);
        controller.open();
        controller.close();
        finish!(initial);
        await Promise.resolve();
        expect(dom.rotate?.hidden).toBe(true);
        expect(controller.startDrag(0.1, 200)).toBe(false);
        expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: 'end' }));
    });

    it('restores native settings in background and starts a new session in foreground', async () => {
        const { controller, request } = setup();
        controller.open();
        await vi.waitFor(() => expect(request).toHaveBeenCalledTimes(1));
        const hidden = vi.spyOn(document, 'hidden', 'get');
        hidden.mockReturnValue(true);
        document.dispatchEvent(new Event('visibilitychange'));
        expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: 'end' }));
        hidden.mockReturnValue(false);
        document.dispatchEvent(new Event('visibilitychange'));
        const begins = request.mock.calls.filter(([call]) => call.operation === 'begin');
        expect(begins).toHaveLength(2);
        expect(begins[0][0].session).not.toBe(begins[1][0].session);
    });

    it('recovers native interruptions even when the document stays visible', async () => {
        const { controller, request, dom } = setup();
        controller.open();
        await vi.waitFor(() => expect(dom.rotate?.hidden).toBe(false));
        const session = request.mock.calls[0][0].session;
        window.dispatchEvent(new CustomEvent('tdrive:player-controls-ended', { detail: { session } }));
        expect(dom.rotate?.hidden).toBe(true);
        expect(request.mock.calls.filter(([call]) => call.operation === 'begin')).toHaveLength(1);
        window.dispatchEvent(new Event('tdrive:player-controls-active'));
        await vi.waitFor(() => expect(dom.rotate?.hidden).toBe(false));
        const begins = request.mock.calls.filter(([call]) => call.operation === 'begin');
        expect(begins).toHaveLength(2);
        expect(begins[1][0].session).not.toBe(session);
    });

    it('uses native rotation and reports native errors instead of pretending success', async () => {
        const request = vi.fn(async (request: MobilePlayerRequest) => {
            if (request.operation === 'rotate') throw new Error('Rotation unavailable');
            return initial;
        });
        const { controller, dom } = setup(request);
        controller.open();
        await vi.waitFor(() => expect(dom.rotate?.hidden).toBe(false));
        dom.rotate!.click();
        await vi.waitFor(() => expect(dom.feedback?.textContent).toBe('Rotation unavailable'));
        expect(dom.rotate?.disabled).toBe(false);
    });
});
