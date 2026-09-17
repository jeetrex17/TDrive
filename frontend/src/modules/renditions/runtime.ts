import { closeGalleryImages, fetchRendition, openGalleryImages, RenditionError, type GalleryImageSession } from '../../api/renditions';
import { onRuntimeEvent } from '../../api/runtime';
import { state } from '../../state';
import { getGalleryPolicy, subscribeGalleryPolicy } from '../gallery-policy';
import { RenditionBroker, type RenditionLease, type RenditionPriority, type RenditionRequest } from './broker';

export type ImageRequest = Omit<RenditionRequest, 'scope'>;
type ImageScope = {
    channelId: number;
    accountId: number;
    nonce: string;
    session: Promise<GalleryImageSession> | null;
    broker: RenditionBroker;
    closed: boolean;
};
let current: ImageScope | null = null;
// Telegram waits belong to the account, not the disposable image session.
// Keep a tiny bounded set so background/pressure cannot shorten its deadline.
const accountCooldowns = new Map<number, number>();
function setCooldown(accountId: number, milliseconds: number): void {
    const now = Date.now();
    for (const [key, deadline] of accountCooldowns) if (deadline <= now) accountCooldowns.delete(key);
    accountCooldowns.set(accountId, Math.max(accountCooldowns.get(accountId) ?? 0, now + milliseconds));
    while (accountCooldowns.size > 8) accountCooldowns.delete(accountCooldowns.keys().next().value!);
}
const resetListeners = new Set<() => void>();
export function subscribeRenditionReset(listener: () => void): () => void {
    resetListeners.add(listener);
    return () => { resetListeners.delete(listener); };
}
let unsubscribePolicy: (() => void) | null = null;
let unsubscribeLock: (() => void) | null = null;
let unsubscribePressure: (() => void) | null = null;

function createScope(channelId: number): ImageScope {
    const scope: ImageScope = {
        channelId, accountId: state.myUserID, nonce: crypto.randomUUID(), session: null, closed: false,
        broker: new RenditionBroker((request, signal) => loadScopedRendition(scope, request, signal), getGalleryPolicy()),
    };
    return scope;
}

async function loadScopedRendition(scope: ImageScope, request: RenditionRequest, signal: AbortSignal) {
    for (let attempt = 0; attempt < 2; attempt += 1) {
        const retryAfterMs = (accountCooldowns.get(scope.accountId) ?? 0) - Date.now();
        if (retryAfterMs > 0) throw new RenditionError('rate_limited', 'Photo loading is temporarily paused by Telegram.', retryAfterMs);
        scope.session ??= openGalleryImages(scope.channelId).catch(error => { scope.session = null; throw error; });
        const opening = scope.session;
        const session = await opening;
        if (scope.closed || signal.aborted) throw new DOMException('Image session released.', 'AbortError');
        try {
            return await fetchRendition(session, request, signal);
        } catch (error) {
            if (error instanceof RenditionError && error.retryAfterMs > 0) setCooldown(scope.accountId, error.retryAfterMs);
            if (!(error instanceof RenditionError) || error.code !== 'session_revoked' || attempt > 0 || scope.closed || signal.aborted) throw error;
            // Idle session expiry is recoverable once. Concurrent expired reads
            // share the replacement promise and cannot discard its fresh token.
            if (scope.session === opening) {
                scope.session = null;
                void closeGalleryImages(session.token).catch(() => {});
            }
        }
    }
    throw new Error('Photo session unavailable.');
}

function watchLifecycle(): void {
    unsubscribePolicy ??= subscribeGalleryPolicy(policy => {
        current?.broker.setLimits(policy);
        if (!policy.allowPrefetch) current?.broker.cancelPrefetch();
        if (policy.backgrounded) resetRenditions();
    });
    unsubscribeLock ??= onRuntimeEvent('encrypted_media_sessions_closed', resetRenditions);
    unsubscribePressure ??= onRuntimeEvent('gallery_memory_pressure', resetRenditions);
}

/** Shared by the gallery and viewer: the last lease cancels unfinished work. */
export function acquireRendition(request: ImageRequest, priority: RenditionPriority): RenditionLease {
    watchLifecycle();
    if (current && (current.channelId !== request.channelId || current.accountId !== state.myUserID)) resetRenditions();
    current ??= createScope(request.channelId);
    return current.broker.acquire({ ...request, scope: current.nonce }, priority);
}

/** Call on lock, logout, drive switch, or background to revoke every local URL. */
export function resetRenditions(): void {
    const old = current;
    current = null;
    if (!old) return;
    old.closed = true;
    old.broker.dispose();
    for (const listener of resetListeners) listener();
    if (old.session) void old.session.then(session => closeGalleryImages(session.token)).catch(() => {});
}

export function renditionStats(): ReturnType<RenditionBroker['stats']> | null { return current?.broker.stats() ?? null; }

/** Host teardown, used when replacing the application shell. */
export function teardownRenditions(): void {
    resetRenditions();
    unsubscribePolicy?.();
    unsubscribePolicy = null;
    unsubscribeLock?.();
    unsubscribeLock = null;
    unsubscribePressure?.();
    unsubscribePressure = null;
}
