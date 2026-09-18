// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const bridge = vi.hoisted(() => ({ callBridge: vi.fn(), hasBridgeMethod: vi.fn() }));
vi.mock('../android-bridge', () => bridge);

import { listNativePhotoBackupAssets, listNativePhotoBackupSources, materializeNativePhotoBackupAsset, nativePhotoBackupFolderPicking, pickNativePhotoBackupFolder } from './native-adapter';

type IOSWindow = Window & { tdriveIOSPhotos?: unknown };

describe('native photo backup adapter', () => {
    beforeEach(() => { bridge.callBridge.mockReset(); bridge.hasBridgeMethod.mockReturnValue(true); });
    afterEach(() => { delete (window as IOSWindow).tdriveIOSPhotos; });

    it('preserves Android MediaStore assets, their capture time and the object cursor', async () => {
        bridge.callBridge.mockResolvedValue(JSON.stringify({
            assets: [{ id: 'media:image:9', version: '100:32', name: 'IMG_9.jpg', mediaType: 'image', modifiedAt: 100, createdAt: 40, size: 32, resourceID: 'media:image:9' }],
            nextCursor: { modified: 100, id: 9 },
        }));
        const first = await listNativePhotoBackupAssets('library:all');
        expect(first.assets[0]).toMatchObject({ id: 'media:image:9', mediaType: 'photo', modifiedAt: 100, createdAt: 40, resourceId: 'media:image:9' });
        await listNativePhotoBackupAssets('library:all', first.nextCursor);
        expect(JSON.parse(bridge.callBridge.mock.calls[1][1][0]).cursor).toEqual({ modified: 100, id: 9 });
    });

    it('carries the subfolder a watched-folder asset came out of', async () => {
        bridge.callBridge.mockResolvedValue(JSON.stringify({
            assets: [{ id: 'media:image:9', version: '100:32', name: 'IMG_9.jpg', mediaType: 'image', relDir: 'Trips/Rome', size: 32, resourceID: 'media:image:9' }],
            nextCursor: null,
        }));
        const page = await listNativePhotoBackupAssets('tree:external_primary:DCIM/');
        expect(page.assets[0].relDir).toBe('Trips/Rome');
    });

    it('reads a picked folder as a source, and a dismissal as nothing', async () => {
        bridge.callBridge.mockResolvedValueOnce(JSON.stringify({ id: 'tree:external_primary:DCIM/Camera/', root: 'external_primary:DCIM/Camera/', name: 'Camera', kind: 'device-folder' }));
        expect(await pickNativePhotoBackupFolder()).toMatchObject({ id: 'tree:external_primary:DCIM/Camera/', kind: 'device-folder', name: 'Camera', enabled: true });
        bridge.callBridge.mockResolvedValueOnce('');
        expect(await pickNativePhotoBackupFolder()).toBeNull();
    });

    // iOS folders come from the Files picker rather than a media index, so the
    // bridge that offers them is the phone's own and not the Android one.
    it('picks a folder through the iOS bridge when that is the host', async () => {
        const pickPhotoBackupFolder = vi.fn().mockResolvedValue({
            id: 'tree:files:private/var/mobile/Camera/', root: 'files:private/var/mobile/Camera/',
            kind: 'device-folder', name: 'Camera',
        });
        (window as IOSWindow).tdriveIOSPhotos = { pickPhotoBackupFolder };
        expect(nativePhotoBackupFolderPicking()).toBe(true);
        expect(await pickNativePhotoBackupFolder()).toMatchObject({
            id: 'tree:files:private/var/mobile/Camera/', root: 'files:private/var/mobile/Camera/', name: 'Camera', enabled: true,
        });
        expect(bridge.callBridge).not.toHaveBeenCalled();
        // A dismissal answers with nothing at all, which is not a folder.
        pickPhotoBackupFolder.mockResolvedValue({});
        expect(await pickNativePhotoBackupFolder()).toBeNull();
    });

    it('reports the grant alongside a list that partial access has shortened', async () => {
        bridge.callBridge.mockResolvedValue(JSON.stringify({
            access: { status: 'limited', detail: 'TDrive can only see the photos you picked.' },
            sources: [{ id: 'all', root: 'content://media', name: 'All photos and videos', kind: 'library' }],
        }));
        const listing = await listNativePhotoBackupSources();
        expect(listing.sources).toHaveLength(1);
        expect(listing.access).toEqual({ status: 'limited', detail: 'TDrive can only see the photos you picked.' });
    });

    it('keeps iOS Live Photo resources distinct and reads their capture time', async () => {
        (window as IOSWindow).tdriveIOSPhotos = {
            listPhotoBackupAssets: () => ({ assets: [
                { id: 'PH-1', resource_id: 'still', version: 'v1', name: 'IMG.HEIC', media_type: 'photo', modified_at: 1, created_at: 7, size: 2 },
                { id: 'PH-1', resource_id: 'paired', version: 'v1', name: 'IMG.mov', media_type: 'video', modified_at: 1, size: 3 },
            ], next_cursor: '' }),
        };
        const page = await listNativePhotoBackupAssets('all');
        expect(page.assets.map((asset) => asset.resourceId)).toEqual(['still', 'paired']);
        expect(page.assets.map((asset) => asset.createdAt)).toEqual([7, 0]);
    });

    it('starts an iOS scan over when the bridge has dropped its snapshot', async () => {
        const listPhotoBackupAssets = vi.fn()
            .mockRejectedValueOnce(Object.assign(new Error('snapshot gone'), { code: 'cursorExpired' }))
            .mockResolvedValueOnce({ assets: [{ id: 'PH-2', version: 'v1', name: 'a.jpg', media_type: 'photo' }], next_cursor: 'tok:1:0' });
        (window as IOSWindow).tdriveIOSPhotos = { listPhotoBackupAssets };
        const page = await listNativePhotoBackupAssets('all', 'tok:9:0');
        expect(page.assets).toHaveLength(1);
        expect(listPhotoBackupAssets.mock.calls.map((call) => call[1])).toEqual(['tok:9:0', '']);
    });

    it('does not mask other iOS failures, or an expiry on a fresh scan, as a restart', async () => {
        const listPhotoBackupAssets = vi.fn().mockRejectedValue(Object.assign(new Error('denied'), { code: 'photoAccessRequired' }));
        (window as IOSWindow).tdriveIOSPhotos = { listPhotoBackupAssets };
        await expect(listNativePhotoBackupAssets('all', 'tok:9:0')).rejects.toThrow('denied');
        listPhotoBackupAssets.mockRejectedValue(Object.assign(new Error('gone'), { code: 'cursorExpired' }));
        await expect(listNativePhotoBackupAssets('all', '')).rejects.toThrow('gone');
        expect(listPhotoBackupAssets).toHaveBeenCalledTimes(2);
    });

    it('cancels the actual iCloud request and releases a late native result', async () => {
        let finish!: (value: unknown) => void;
        const pending = Object.assign(new Promise((resolve) => { finish = resolve; }), { requestID: 'request-7' });
        const cancelMaterialization = vi.fn();
        const releasePhotoBackupResource = vi.fn();
        (window as IOSWindow).tdriveIOSPhotos = {
            materializePhotoBackupResource: () => pending,
            cancelMaterialization,
            releasePhotoBackupResource,
        };
        const controller = new AbortController();
        const result = materializeNativePhotoBackupAsset({ id: 'PH-1', version: 'v1', name: 'IMG.HEIC', mediaType: 'photo', modifiedAt: 1, createdAt: 0, size: 0 }, controller.signal);
        const rejected = expect(result).rejects.toThrow('Backup paused');
        controller.abort();
        expect(cancelMaterialization).toHaveBeenCalledWith('request-7');
        // Native staging handles identify temporary files; they are not credentials.
        const stagingHandle = 'native-resource-7';
        finish({ path: '/cache/IMG.HEIC', token: stagingHandle });
        await rejected;
        expect(releasePhotoBackupResource).toHaveBeenCalledWith(stagingHandle);
    });
});
