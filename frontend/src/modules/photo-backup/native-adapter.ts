import { callBridge, hasBridgeMethod } from '../android-bridge';
import type { PhotoBackupAsset, PhotoBackupSource } from '../../api/photo-backup';
import { asRecord, boundedText, nonNegativeNumber } from '../../api/shared';

export interface NativeAssetPage { assets: PhotoBackupAsset[]; nextCursor: string; }
/** What the host knows about the media grant, for the one line the panel shows. */
export interface NativeAccess { status: string; detail: string; }
export interface NativeSourceListing { sources: PhotoBackupSource[]; access: NativeAccess; }
export interface NativeMaterializedResource { path: string; releaseID: string; }
interface IOSPhotosBridge { listPhotoBackupSources?: (cursor?: string, limit?: number) => unknown; listPhotoBackupAssets?: (sourceID: string, cursor: string, limit: number) => unknown; pickPhotoBackupFolder?: () => unknown; materializePhotoBackupResource?: (assetID: string, version: string, maxBytes?: number, resourceID?: string) => unknown; releasePhotoBackupResource?: (token: string) => unknown; cancelMaterialization?: (requestID: string) => unknown; setBackgroundBackup?: (active: boolean) => unknown; }
function iosBridge(): IOSPhotosBridge | null { return typeof window !== 'undefined' ? (window as Window & { tdriveIOSPhotos?: IOSPhotosBridge }).tdriveIOSPhotos ?? null : null; }
async function nativeCall(value: unknown): Promise<Record<string, unknown>> { const result = await Promise.resolve(value); return typeof result === 'string' ? parse(result) : asRecord(result); }

function parse(raw: string): Record<string, unknown> { try { return asRecord(JSON.parse(raw || '{}')); } catch { return {}; } }

/** The iOS bridge rejects with an Error carrying a stable `code`; anything else reads as ''. */
export function nativeErrorCode(cause: unknown): string {
    const code = (cause as { code?: unknown } | null)?.code;
    return typeof code === 'string' ? code : '';
}

// The Android host currently owns these names. iOS uses the same semantic
// contract through its bridge, keeping enumeration and staging out of UI code.
export function nativePhotoBackupAvailable(): boolean { return hasBridgeMethod('listPhotoBackupAssets') || Boolean(iosBridge()?.listPhotoBackupAssets); }

const PAGE_LIMIT = 128;

function toAssetPage(raw: Record<string, unknown>): NativeAssetPage {
    const assets = Array.isArray(raw.assets) ? raw.assets.map((value): PhotoBackupAsset | null => {
        const asset = asRecord(value); const id = boundedText(asset.id, 512); const version = boundedText(asset.version, 256); const rawType = asset.media_type ?? asset.mediaType; const mediaType = rawType === 'video' ? 'video' : rawType === 'photo' || rawType === 'image' ? 'photo' : null;
        return id && version && mediaType ? { id, version, name: boundedText(asset.name, 512) || 'Untitled', mediaType, modifiedAt: nonNegativeNumber(asset.modified_at ?? asset.modifiedAt), createdAt: nonNegativeNumber(asset.created_at ?? asset.createdAt), size: nonNegativeNumber(asset.size), resourceId: boundedText(asset.resource_id ?? asset.resourceID, 512) || undefined, relDir: boundedText(asset.rel_dir ?? asset.relDir, 1024) || undefined } : null;
    }).filter((asset): asset is PhotoBackupAsset => asset !== null) : [];
    const next = raw.nextCursor ?? raw.next_cursor;
    return { assets, nextCursor: typeof next === 'string' ? boundedText(next, 1024) : next && typeof next === 'object' ? JSON.stringify(next) : '' };
}

export async function listNativePhotoBackupAssets(sourceID: string, cursor = ''): Promise<NativeAssetPage> {
    const ios = iosBridge();
    if (ios?.listPhotoBackupAssets) {
        try {
            return toAssetPage(await nativeCall(ios.listPhotoBackupAssets(sourceID, cursor, PAGE_LIMIT)));
        } catch (cause) {
            // The bridge pages over a library snapshot it keeps per cursor and
            // lets go of when the app is suspended or memory runs short. Starting
            // over is cheap: the ledger ignores everything it already holds.
            if (!cursor || nativeErrorCode(cause) !== 'cursorExpired') throw cause;
            return toAssetPage(await nativeCall(ios.listPhotoBackupAssets(sourceID, '', PAGE_LIMIT)));
        }
    }
    let nativeCursor: unknown = cursor;
    try { nativeCursor = cursor ? JSON.parse(cursor) : ''; } catch { /* opaque cursors remain strings */ }
    return toAssetPage(parse(await callBridge('listPhotoBackupAssets', [JSON.stringify({ sourceId: sourceID, cursor: nativeCursor, limit: PAGE_LIMIT })], 'Photo library access is unavailable.')));
}

export async function requestNativePhotoBackupAccess(): Promise<void> {
    const ios = iosBridge() as (IOSPhotosBridge & { requestAuthorization?: () => unknown }) | null;
    if (ios?.requestAuthorization) { await nativeCall(ios.requestAuthorization()); return; }
    if (hasBridgeMethod('requestPhotoBackupAccess')) await callBridge('requestPhotoBackupAccess', [], 'Photo library access is unavailable.');
}

export async function nativePhotoBackupPolicy(): Promise<boolean | null> {
    const ios = iosBridge() as (IOSPhotosBridge & { getPhotoBackupCapabilities?: () => unknown }) | null;
    const raw = ios?.getPhotoBackupCapabilities
        ? await nativeCall(ios.getPhotoBackupCapabilities())
        : hasBridgeMethod('photoBackupPolicyStatus')
            ? parse(await callBridge('photoBackupPolicyStatus', [], ''))
            : null;
    if (!raw) return null;
    return raw.wifi === true || raw.wifi_status === 'wifi';
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

export async function listNativePhotoBackupSources(): Promise<NativeSourceListing> {
    const ios = iosBridge();
    if (!hasBridgeMethod('listPhotoBackupSources') && !ios?.listPhotoBackupSources) return { sources: [], access: { status: '', detail: '' } };
    let raw: Record<string, unknown>;
    let entries: unknown[];
    if (ios?.listPhotoBackupSources) {
        // The iOS shim pages the album list; the first page alone stopped at
        // the 49th album and quietly left the rest of a big library out.
        raw = await nativeCall(ios.listPhotoBackupSources('', 50));
        entries = Array.isArray(raw.sources) ? [...raw.sources] : [];
        let cursor = boundedText(raw.nextCursor, 64);
        for (let page = 0; cursor && page < 40; page += 1) {
            const more = await nativeCall(ios.listPhotoBackupSources(cursor, 50));
            if (Array.isArray(more.sources)) entries.push(...more.sources);
            const next = boundedText(more.nextCursor, 64);
            cursor = next === cursor ? '' : next;
        }
    } else {
        raw = parse(await callBridge('listPhotoBackupSources', [], 'Photo library access is unavailable.'));
        entries = Array.isArray(raw.sources) ? raw.sources : [];
    }
    const sources = entries.map((value): PhotoBackupSource | null => { const source = asRecord(value); const id = boundedText(source.id, 512); const root = boundedText(source.root, 1024); return id && root ? { id, kind: boundedText(source.kind, 64), name: boundedText(source.name, 240) || 'Photo library', root, enabled: source.enabled !== false, addedAt: nonNegativeNumber(source.added_at) } : null; }).filter((source): source is PhotoBackupSource => source !== null);
    // The grant is only knowable here. The backend cannot see an Android
    // permission, so the panel's access line is carried from the same call that
    // produced the (possibly very short) list it explains.
    const access = asRecord(raw.access);
    return { sources, access: { status: boundedText(access.status, 64), detail: boundedText(access.detail, 240) } };
}

/** Whether this host can be asked for a folder to back up. */
export function nativePhotoBackupFolderPicking(): boolean {
    return hasBridgeMethod('pickPhotoBackupFolder') || Boolean(iosBridge()?.pickPhotoBackupFolder);
}

/**
 * Opens the system folder picker and resolves with the source the chosen
 * folder becomes, or null when it was dismissed. The host refuses a folder it
 * cannot read later -- one from a cloud provider, or on storage that is gone --
 * and rejects with its own words, which are written for the user.
 */
export async function pickNativePhotoBackupFolder(): Promise<PhotoBackupSource | null> {
    const ios = iosBridge();
    const raw = ios?.pickPhotoBackupFolder
        ? await nativeCall(ios.pickPhotoBackupFolder())
        : parse(await callBridge('pickPhotoBackupFolder', [], 'This build cannot open a folder picker.'));
    const id = boundedText(raw.id, 512);
    const root = boundedText(raw.root, 1024);
    if (!id || !root) return null;
    return { id, kind: boundedText(raw.kind, 64) || 'device-folder', name: boundedText(raw.name, 240) || 'Folder', root, enabled: true, addedAt: 0 };
}
