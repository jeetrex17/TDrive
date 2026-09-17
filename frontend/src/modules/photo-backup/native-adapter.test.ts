// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const bridge = vi.hoisted(() => ({ callBridge: vi.fn(), hasBridgeMethod: vi.fn() }));
vi.mock('../android-bridge', () => bridge);

import { listNativePhotoBackupAssets, materializeNativePhotoBackupAsset } from './native-adapter';

describe('native photo backup adapter', () => {
    beforeEach(() => { bridge.callBridge.mockReset(); bridge.hasBridgeMethod.mockReturnValue(true); });
    afterEach(() => { delete (window as Window & { tdriveIOSPhotos?: unknown }).tdriveIOSPhotos; });

    it('preserves Android MediaStore assets and its object cursor', async () => {
        bridge.callBridge.mockResolvedValue(JSON.stringify({
            assets: [{ id: 'media:image:9', version: '100:32', name: 'IMG_9.jpg', mediaType: 'image', modifiedAt: 100, size: 32, resourceID: 'media:image:9' }],
            nextCursor: { modified: 100, id: 9 },
        }));
        const first = await listNativePhotoBackupAssets('library:all');
        expect(first.assets[0]).toMatchObject({ id: 'media:image:9', mediaType: 'photo', modifiedAt: 100, resourceId: 'media:image:9' });
        await listNativePhotoBackupAssets('library:all', first.nextCursor);
        expect(JSON.parse(bridge.callBridge.mock.calls[1][1][0]).cursor).toEqual({ modified: 100, id: 9 });
    });

    it('keeps iOS Live Photo resources distinct', async () => {
        (window as Window & { tdriveIOSPhotos?: unknown }).tdriveIOSPhotos = {
            listPhotoBackupAssets: () => ({ assets: [
                { id: 'PH-1', resource_id: 'still', version: 'v1', name: 'IMG.HEIC', media_type: 'photo', modified_at: 1, size: 2 },
                { id: 'PH-1', resource_id: 'paired', version: 'v1', name: 'IMG.mov', media_type: 'video', modified_at: 1, size: 3 },
            ], next_cursor: '' }),
        };
        const page = await listNativePhotoBackupAssets('all');
        expect(page.assets.map((asset) => asset.resourceId)).toEqual(['still', 'paired']);
        delete (window as Window & { tdriveIOSPhotos?: unknown }).tdriveIOSPhotos;
    });

    it('cancels the actual iCloud request and releases a late native result', async () => {
        let finish!: (value: unknown) => void;
        const pending = Object.assign(new Promise((resolve) => { finish = resolve; }), { requestID: 'request-7' });
        const cancelMaterialization = vi.fn();
        const releasePhotoBackupResource = vi.fn();
        (window as Window & { tdriveIOSPhotos?: unknown }).tdriveIOSPhotos = {
            materializePhotoBackupResource: () => pending,
            cancelMaterialization,
            releasePhotoBackupResource,
        };
        const controller = new AbortController();
        const result = materializeNativePhotoBackupAsset({ id: 'PH-1', version: 'v1', name: 'IMG.HEIC', mediaType: 'photo', modifiedAt: 1, size: 0 }, controller.signal);
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
