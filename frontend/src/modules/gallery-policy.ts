import { isMobilePlatform, onRuntimeEvent } from '../api/runtime';
import type { RenditionLimits } from './renditions/broker';

export interface GallerySignals {
    connected?: boolean;
    metered?: boolean;
    constrained?: boolean;
    lowPowerMode?: boolean;
    backgrounded?: boolean;
    memoryPressure?: boolean;
}
export interface GalleryPolicy extends RenditionLimits {
    allowPrefetch: boolean;
    backgrounded: boolean;
}
const MiB = 1024 * 1024;
const originalDecodedReserve = 128 * MiB;
const originalCompressedReserve = 8 * MiB;
const minimumViewerThumbnailDecoded = 16 * MiB;
const minimumViewerThumbnailCompressed = 2 * MiB;
const signalKeys = ['connected', 'metered', 'constrained', 'lowPowerMode', 'backgrounded', 'memoryPressure'] as const;

/** Unknown network cost deliberately disables speculation, including on desktop. */
export function deriveGalleryPolicy(mobile: boolean, signals: GallerySignals): GalleryPolicy {
    const backgrounded = signals.backgrounded === true;
    const reduced = signals.lowPowerMode === true || signals.memoryPressure === true;
    return {
        concurrency: backgrounded ? 0 : reduced ? 1 : mobile ? 2 : 4,
        decodedBytes: (signals.memoryPressure ? (mobile ? 48 : 96) : (mobile ? 80 : 192)) * MiB,
        compressedBytes: (signals.memoryPressure ? (mobile ? 4 : 16) : (mobile ? 8 : 32)) * MiB,
        maxQueued: mobile ? 192 : 448,
        backgrounded,
        allowPrefetch: signals.connected === true && signals.metered === false
            && signals.constrained !== true && !reduced && !backgrounded,
    };
}

export function normalizeGallerySignals(payload: unknown): GallerySignals {
    if (typeof payload === 'string') {
        try { return normalizeGallerySignals(JSON.parse(payload)); } catch { return {}; }
    }
    if (!payload || typeof payload !== 'object' || Array.isArray(payload)) return {};
    const normalized = Object.fromEntries(signalKeys.flatMap(key => {
        const value = (payload as Record<string, unknown>)[key];
        return typeof value === 'boolean' ? [[key, value]] : [];
    }));
    const expensive = (payload as Record<string, unknown>).expensive;
    return typeof expensive === 'boolean' && normalized.metered === undefined ? { ...normalized, metered: expensive } : normalized;
}

let signals: GallerySignals = {};
let started = false;
let pressureUntil = 0;
let pressureTimer: ReturnType<typeof setTimeout> | null = null;
let originalViewerReservations = 0;
const pressureCooldownMs = 60_000;
const listeners = new Set<(policy: GalleryPolicy) => void>();
const cleanups: Array<() => void> = [];
const currentVisibility = () => typeof document !== 'undefined' && document.visibilityState === 'hidden';
export const getGalleryPolicy = (): GalleryPolicy => {
    const policy = deriveGalleryPolicy(isMobilePlatform(), { ...signals, backgrounded: signals.backgrounded === true || currentVisibility(), memoryPressure: signals.memoryPressure === true || Date.now() < pressureUntil });
    if (originalViewerReservations === 0) return policy;
    return {
        ...policy,
        decodedBytes: Math.max(minimumViewerThumbnailDecoded, policy.decodedBytes - originalDecodedReserve),
        compressedBytes: Math.max(minimumViewerThumbnailCompressed, policy.compressedBytes - originalCompressedReserve),
    };
};

function publishCurrentPolicy(): void {
    const policy = getGalleryPolicy();
    for (const listener of listeners) listener(policy);
}

function publish(payload: unknown): void {
    signals = { ...signals, ...normalizeGallerySignals(payload) };
    publishCurrentPolicy();
}

/** Shrinks the shared thumbnail cache while an original image is being decoded. */
export function acquireOriginalViewerBudget(): () => void {
    originalViewerReservations += 1;
    publishCurrentPolicy();
    let released = false;
    return () => {
        if (released) return;
        released = true;
        originalViewerReservations = Math.max(0, originalViewerReservations - 1);
        publishCurrentPolicy();
    };
}

function schedulePressureRecovery(): void {
    if (pressureTimer !== null) clearTimeout(pressureTimer);
    const remaining = pressureUntil - Date.now();
    if (remaining <= 0) { pressureUntil = 0; pressureTimer = null; return; }
    pressureTimer = setTimeout(() => {
        pressureTimer = null;
        pressureUntil = 0;
        publish({});
    }, remaining);
}

function onMemoryPressure(): void {
    pressureUntil = Date.now() + pressureCooldownMs;
    schedulePressureRecovery();
    publish({});
}

function start(): void {
    if (started) return;
    started = true;
    for (const event of ['android:NetworkChanged', 'android:BatteryChanged', 'ios:NetworkChanged', 'ios:BatteryChanged'] as const) {
        cleanups.push(onRuntimeEvent(event, publish));
    }
    cleanups.push(onRuntimeEvent('gallery_memory_pressure', onMemoryPressure));
    schedulePressureRecovery();
    if (typeof document !== 'undefined') {
        const visibility = () => publish({ backgrounded: currentVisibility() });
        document.addEventListener('visibilitychange', visibility);
        cleanups.push(() => document.removeEventListener('visibilitychange', visibility));
    }
}

/** Native adapters and tests may submit the same validated signal shape. */
export const updateGallerySignals = publish;

export function subscribeGalleryPolicy(listener: (policy: GalleryPolicy) => void): () => void {
    start();
    listeners.add(listener);
    return () => {
        listeners.delete(listener);
        if (listeners.size > 0) return;
        for (const cleanup of cleanups.splice(0)) cleanup();
        if (pressureTimer !== null) clearTimeout(pressureTimer);
        pressureTimer = null;
        started = false;
    };
}
