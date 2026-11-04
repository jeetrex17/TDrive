import { afterEach, describe, expect, it, vi } from 'vitest';
import { hasMobilePlayerControls, normalizeMobilePlayerState, requestMobilePlayer } from './mobile-player';

type Host = Window & {
    tdrivePlayer?: { postMessage: (message: string) => void };
    webkit?: { messageHandlers: { tdrivePlayer: { postMessage: (message: unknown) => void } } };
    __tdrivePlayerReply?: (reply: { callbackId: string; result?: unknown; error?: string }) => void;
};
const host = window as Host;
afterEach(() => { delete host.tdrivePlayer; delete host.webkit; vi.useRealTimers(); });

describe('mobile player native boundary', () => {
    it('uses the origin-scoped Android listener and resolves only matching replies', async () => {
        const postMessage = vi.fn();
        host.tdrivePlayer = { postMessage };
        const request = { operation: 'begin' as const, session: 'session' };
        const result = requestMobilePlayer(request);
        const envelope = JSON.parse(postMessage.mock.calls[0][0]);
        expect(envelope.request).toEqual(request);
        host.__tdrivePlayerReply!({ callbackId: 'unrelated', result: {} });
        host.__tdrivePlayerReply!({ callbackId: envelope.callbackId, result: { ready: true } });
        await expect(result).resolves.toEqual({ ready: true });
    });

    it('reports native errors and unavailable builds', async () => {
        host.tdrivePlayer = { postMessage: (message) => {
            const { callbackId } = JSON.parse(message);
            host.__tdrivePlayerReply!({ callbackId, error: 'Screen locked' });
        } };
        await expect(requestMobilePlayer({ operation: 'rotate', session: 'session', orientation: 'landscape' })).rejects.toThrow('Screen locked');
        delete host.tdrivePlayer;
        expect(hasMobilePlayerControls()).toBe(false);
        await expect(requestMobilePlayer({ operation: 'begin', session: 'session' })).rejects.toThrow('unavailable');
    });

    it('routes iOS requests to its script handler', async () => {
        const postMessage = vi.fn();
        Object.defineProperty(host, 'webkit', { configurable: true, value: { messageHandlers: { tdrivePlayer: { postMessage } } } });
        const request = { operation: 'showVolume' as const, session: 'ios' };
        const result = requestMobilePlayer(request);
        const envelope = postMessage.mock.calls[0][0];
        expect(envelope.request).toEqual(request);
        host.__tdrivePlayerReply!({ callbackId: envelope.callbackId, result: {} });
        await expect(result).resolves.toEqual({});
    });

    it('times out silent hosts and ignores their late replies', async () => {
        vi.useFakeTimers();
        const postMessage = vi.fn();
        host.tdrivePlayer = { postMessage };
        const result = requestMobilePlayer({ operation: 'begin', session: 'silent' });
        const rejected = expect(result).rejects.toThrow('did not respond');
        await vi.advanceTimersByTimeAsync(5000);
        await rejected;
        const { callbackId } = JSON.parse(postMessage.mock.calls[0][0]);
        expect(() => host.__tdrivePlayerReply!({ callbackId, result: {} })).not.toThrow();
        expect(vi.getTimerCount()).toBe(0);
    });

    it('rejects invalid native levels and capabilities', () => {
        const valid = { brightness: 0.5, volume: 0.5, orientation: 'portrait', volumeMode: 'system' };
        expect(normalizeMobilePlayerState(valid)).toEqual(valid);
        for (const state of [null, { ...valid, brightness: NaN }, { ...valid, volume: 5 }, { ...valid, volumeMode: 'fake' }]) {
            expect(() => normalizeMobilePlayerState(state)).toThrow('Invalid');
        }
    });
});
