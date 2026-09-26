import { afterEach, describe, expect, it, vi } from 'vitest';
const bridge = vi.hoisted(() => ({ open: vi.fn(), close: vi.fn() }));
vi.mock('../../bindings/TDrive/app', () => ({ OpenGalleryImages: bridge.open, CloseGalleryImages: bridge.close }));
import { closeGalleryImages, fetchRendition, openGalleryImages, retryDeadline } from './renditions';
const session = { channelId: 10, token: crypto.randomUUID(), baseUrl: 'http://127.0.0.1:4433/rendition/token' };
const request = { scope: 'session', channelId: 10, fileId: 1, revision: 2, kind: 'thumbnail' as const };
function response(body: BodyInit | null = new Uint8Array([1, 2]), status = 200, headers: Record<string, string> = {}) {
    return new Response(body, { status, headers: { 'X-Rendition-Width': '32', 'X-Rendition-Height': '24', 'Content-Type': 'image/jpeg', ...headers } });
}
afterEach(() => { vi.unstubAllGlobals(); vi.clearAllMocks(); });

describe('binary rendition transport', () => {
    it('checks the backend session drive before exposing a token', async () => {
        bridge.open.mockResolvedValue({ token: 'scope', base_url: 'http://127.0.0.1/images', channel_id: 11 });
        await expect(openGalleryImages(10)).rejects.toThrow('drive changed');
        expect(bridge.close).toHaveBeenCalledWith('scope');
        bridge.open.mockResolvedValue({ token: 'scope', base_url: 'http://127.0.0.1/images', channel_id: 10 });
        expect(await openGalleryImages(10)).toMatchObject({ channelId: 10, token: 'scope' });
        await closeGalleryImages('');
    });

    it('requests scoped binary bytes without a persistent browser cache', async () => {
        const fetcher = vi.fn(async () => response());
        vi.stubGlobal('fetch', fetcher);
        const signal = new AbortController().signal;
        const result = await fetchRendition(session, request, signal);
        expect(result).toMatchObject({ width: 32, height: 24 });
        expect(result.blob.size).toBe(2);
        expect(fetcher).toHaveBeenCalledWith(`${session.baseUrl}/1/thumbnail?revision=2`, { signal, cache: 'no-store', credentials: 'omit' });
        await expect(fetchRendition(session, { ...request, channelId: 12 }, signal)).rejects.toThrow('drive changed');
    });

    it('rejects oversized headers before reading the response', async () => {
        vi.stubGlobal('fetch', vi.fn(async () => response(null, 200, { 'X-Rendition-Width': '100000' })));
        await expect(fetchRendition(session, request, new AbortController().signal)).rejects.toMatchObject({ code: 'too_large' });
    });

    it('bounds streaming responses even when Content-Length is absent', async () => {
        vi.stubGlobal('fetch', vi.fn(async () => response(new Uint8Array(1024 * 1024 + 1))));
        await expect(fetchRendition(session, request, new AbortController().signal)).rejects.toMatchObject({ code: 'too_large' });
    });

    it.each([[404, 'missing_rendition'], [423, 'encryption_password_required'], [429, 'rate_limited'], [410, 'session_revoked'], [413, 'rendition_unavailable']])('classifies HTTP %i without exposing server bodies', async (status, code) => {
        vi.stubGlobal('fetch', vi.fn(async () => response('private server details', Number(status), { 'Retry-After': '3701' })));
        await expect(fetchRendition(session, request, new AbortController().signal)).rejects.toMatchObject({ code, retryAfterMs: 3_701_000 });
    });

    it('rejects missing binary bodies', async () => {
        vi.stubGlobal('fetch', vi.fn(async () => response(null)));
        await expect(fetchRendition(session, request, new AbortController().signal)).rejects.toThrow('Empty');
    });

    it('preserves server wait deadlines in seconds and HTTP dates', () => {
        expect(retryDeadline('3701')).toBe(3_701_000);
        expect(retryDeadline('Thu, 17 Sep 2026 12:00:00 GMT', Date.parse('2026-09-17T11:00:00Z'))).toBe(3_600_000);
        expect(retryDeadline('not a date')).toBe(0);
        expect(retryDeadline(null)).toBe(0);
    });
});
