import { callBridge, hasBridgeMethod } from '../android-bridge';
import type { PhotoBackupAsset, PhotoBackupSource } from '../../api/photo-backup';
import { asRecord, boundedText, nonNegativeNumber } from '../../api/shared';

export interface NativeAssetPage { assets: PhotoBackupAsset[]; nextCursor: string; }
export interface NativeMaterializedResource { path: string; releaseID: string; }
interface IOSPhotosBridge { listPhotoBackupSources?: () => unknown; listPhotoBackupAssets?: (sourceID: string, cursor: string, limit: number) => unknown; materializePhotoBackupResource?: (assetID: string, version: string, maxBytes?: number, resourceID?: string) => unknown; releasePhotoBackupResource?: (token: string) => unknown; cancelMaterialization?: (requestID: string) => unknown; setBackgroundBackup?: (active: boolean) => unknown; }
function iosBridge(): IOSPhotosBridge | null { return typeof window !== 'undefined' ? (window as Window & { tdriveIOSPhotos?: IOSPhotosBridge }).tdriveIOSPhotos ?? null : null; }
async function nativeCall(value: unknown): Promise<Record<string, unknown>> { const result = await Promise.resolve(value); return typeof result === 'string' ? parse(result) : asRecord(result); }

function parse(raw: string): Record<string, unknown> { try { return asRecord(JSON.parse(raw || '{}')); } catch { return {}; } }

// The Android host currently owns these names. iOS uses the same semantic
// contract through its bridge, keeping enumeration and staging out of UI code.
export function nativePhotoBackupAvailable(): boolean { return hasBridgeMethod('listPhotoBackupAssets') || Boolean(iosBridge()?.listPhotoBackupAssets); }

export async function listNativePhotoBackupAssets(sourceID: string, cursor = ''): Promise<NativeAssetPage> {
    const ios = iosBridge();
    let nativeCursor: unknown = cursor;
    try { nativeCursor = cursor ? JSON.parse(cursor) : ''; } catch { /* opaque cursors remain strings */ }
    const raw = ios?.listPhotoBackupAssets
        ? await nativeCall(ios.listPhotoBackupAssets(sourceID, cursor, 128))
        : parse(await callBridge('listPhotoBackupAssets', [JSON.stringify({ sourceId: sourceID, cursor: nativeCursor, limit: 128 })], 'Photo library access is unavailable.'));
    const assets = Array.isArray(raw.assets) ? raw.assets.map((value): PhotoBackupAsset | null => {
        const asset = asRecord(value); const id = boundedText(asset.id, 512); const version = boundedText(asset.version, 256); const rawType = asset.media_type ?? asset.mediaType; const mediaType = rawType === 'video' ? 'video' : rawType === 'photo' || rawType === 'image' ? 'photo' : null;
        return id && version && mediaType ? { id, version, name: boundedText(asset.name, 512) || 'Untitled', mediaType, modifiedAt: nonNegativeNumber(asset.modified_at ?? asset.modifiedAt), size: nonNegativeNumber(asset.size), resourceId: boundedText(asset.resource_id ?? asset.resourceID, 512) || undefined } : null;
    }).filter((asset): asset is PhotoBackupAsset => asset !== null) : [];
    const next = raw.nextCursor ?? raw.next_cursor;
    return { assets, nextCursor: typeof next === 'string' ? boundedText(next, 1024) : next && typeof next === 'object' ? JSON.stringify(next) : '' };
}

export async function requestNativePhotoBackupAccess(): Promise<void> {
    const ios = iosBridge() as (IOSPhotosBridge & { requestAuthorization?: () => unknown }) | null;
    if (ios?.requestAuthorization) { await nativeCall(ios.requestAuthorization()); return; }
    if (hasBridgeMethod('requestPhotoBackupAccess')) await callBridge('requestPhotoBackupAccess', [], 'Photo library access is unavailable.');
}

export async function nativePhotoBackupPolicy(): Promise<{ wifi: boolean; charging: boolean } | null> {
    const ios = iosBridge() as (IOSPhotosBridge & { getPhotoBackupCapabilities?: () => unknown }) | null;
    const raw = ios?.getPhotoBackupCapabilities
        ? await nativeCall(ios.getPhotoBackupCapabilities())
        : hasBridgeMethod('photoBackupPolicyStatus')
            ? parse(await callBridge('photoBackupPolicyStatus', [], ''))
            : null;
    if (!raw) return null;
    const wifi = raw.wifi === true || raw.wifi_status === 'wifi';
    const charging = raw.charging === true || raw.charging_status === 'charging';
    return { wifi, charging };
}

export async function setIOSPhotoBackupBackground(active: boolean): Promise<boolean> {
    const operation = iosBridge()?.setBackgroundBackup;
    if (!operation) return false;
    await nativeCall(operation(active));
    return true;
}

export async function materializeNativePhotoBackupAsset(asset: PhotoBackupAsset, signal?: AbortSignal): Promise<NativeMaterializedResource> {
    if (signal?.aborted) throw new Error('Backup paused.');
    const ios = iosBridge();
    const request = ios?.materializePhotoBackupResource
        ? ios.materializePhotoBackupResource(asset.id, asset.version, 4 * 1024 ** 3, asset.resourceId)
        : callBridge('materializePhotoBackupAsset', [JSON.stringify({ id: asset.id, version: asset.version })], 'This build cannot read the selected media.');
    const cancel = () => {
        const requestID = (request as { requestID?: string })?.requestID;
        const operation = ios && requestID
            ? ios.cancelMaterialization?.(requestID)
            : releaseNativePhotoBackupAsset(asset.id);
        void Promise.resolve(operation).catch(() => undefined);
    };
    signal?.addEventListener('abort', cancel, { once: true });
    try {
        const raw = await nativeCall(request);
        const result = { path: boundedText(raw.path, 4096), releaseID: boundedText(raw.token, 512) || asset.id };
        if (signal?.aborted) {
            await releaseNativePhotoBackupAsset(result.releaseID);
            throw new Error('Backup paused.');
        }
        return result;
    } finally { signal?.removeEventListener('abort', cancel); }
}

export async function releaseNativePhotoBackupAsset(releaseID: string): Promise<void> {
    const ios = iosBridge();
    if (ios?.releasePhotoBackupResource) { await nativeCall(ios.releasePhotoBackupResource(releaseID)); return; }
    if (!hasBridgeMethod('releasePhotoBackupAsset')) return;
    await callBridge('releasePhotoBackupAsset', [JSON.stringify({ id: releaseID })], '');
}

export async function listNativePhotoBackupSources(): Promise<PhotoBackupSource[]> {
    const ios = iosBridge();
    if (!hasBridgeMethod('listPhotoBackupSources') && !ios?.listPhotoBackupSources) return [];
    const raw = ios?.listPhotoBackupSources ? await nativeCall(ios.listPhotoBackupSources()) : parse(await callBridge('listPhotoBackupSources', [], 'Photo library access is unavailable.'));
    const entries = Array.isArray(raw.sources) ? raw.sources : [];
    return entries.map((value): PhotoBackupSource | null => { const source = asRecord(value); const id = boundedText(source.id, 512); const root = boundedText(source.root, 1024); return id && root ? { id, kind: boundedText(source.kind, 64), name: boundedText(source.name, 240) || 'Photo library', root, enabled: source.enabled !== false, addedAt: nonNegativeNumber(source.added_at) } : null; }).filter((source): source is PhotoBackupSource => source !== null);
}
