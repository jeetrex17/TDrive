import { CloseGalleryImages, OpenGalleryImages } from '../../bindings/TDrive/app';
import { invokeBackend } from './gateway';
import { renditionMaxBytes, renditionMaxEdge, type RenditionBytes, type RenditionRequest } from '../modules/renditions/broker';

export interface GalleryImageSession { token: string; baseUrl: string; channelId: number }
export class RenditionError extends Error {
    constructor(readonly code: string, message: string, readonly retryAfterMs = 0) { super(message); }
}

export async function openGalleryImages(channelId: number): Promise<GalleryImageSession> {
    const opened = await invokeBackend(OpenGalleryImages, channelId);
    const session = { token: String(opened.token), baseUrl: String(opened.base_url), channelId: Number(opened.channel_id) };
    if (!session.token || session.channelId !== channelId) {
        if (session.token) await closeGalleryImages(session.token);
        throw new Error('The active drive changed.');
    }
    return session;
}

export async function closeGalleryImages(token: string): Promise<void> {
    if (token) await invokeBackend(CloseGalleryImages, token);
}

/** Stream into a bounded binary buffer; never shuttle pixels through bridge JSON. */
export async function fetchRendition(session: GalleryImageSession, request: RenditionRequest, signal: AbortSignal): Promise<RenditionBytes> {
    if (session.channelId !== request.channelId) throw new Error('The active drive changed.');
    const response = await fetch(`${session.baseUrl}/${request.fileId}/${request.kind}?revision=${request.revision}`, { signal, cache: 'no-store', credentials: 'omit' });
    if (!response.ok) {
        await response.body?.cancel();
        const code = response.status === 423 ? 'encryption_password_required' : response.status === 404 ? 'missing_rendition' : response.status === 429 ? 'rate_limited' : response.status === 410 ? 'session_revoked' : 'rendition_unavailable';
        const message = response.status === 404 ? 'A preview is not available yet. Download the original to view this photo.'
            : response.status === 413 ? 'This photo exceeds the safe preview size. Download the original to view it.'
            : response.status === 423 ? 'Unlock this drive to view the photo.' : 'Photo preview is unavailable. Try again.';
        throw new RenditionError(code, message, retryDeadline(response.headers.get('Retry-After')));
    }
    const width = Number(response.headers.get('X-Rendition-Width'));
    const height = Number(response.headers.get('X-Rendition-Height'));
    const maxBytes = renditionMaxBytes(request.kind);
    const edge = renditionMaxEdge(request.kind);
    const declaredSize = Number(response.headers.get('Content-Length'));
    if (!Number.isInteger(width) || !Number.isInteger(height) || width < 1 || height < 1 || width > edge || height > edge || declaredSize > maxBytes) {
        await response.body?.cancel();
        throw new RenditionError('too_large', 'This photo exceeds the safe preview size.');
    }
    if (!response.body) throw new Error('Empty photo preview.');
    const reader = response.body.getReader();
    const chunks: ArrayBuffer[] = [];
    let received = 0;
    try {
        while (true) {
            const { value, done } = await reader.read();
            if (done) break;
            received += value.byteLength;
            if (received > maxBytes) throw new RenditionError('too_large', 'This photo exceeds the safe preview size.');
            chunks.push(value.slice().buffer);
        }
    } catch (error) {
        await reader.cancel().catch(() => {});
        throw error;
    } finally { reader.releaseLock(); }
    return { blob: new Blob(chunks, { type: response.headers.get('Content-Type') || 'image/jpeg' }), width, height };
}

/** Retry-After may be seconds or an HTTP date; never truncate server waits. */
export function retryDeadline(value: string | null, now = Date.now()): number {
    if (!value) return 0;
    const seconds = Number(value);
    if (Number.isFinite(seconds) && seconds >= 0) return seconds * 1000;
    const date = Date.parse(value);
    return Number.isFinite(date) ? Math.max(0, date - now) : 0;
}
