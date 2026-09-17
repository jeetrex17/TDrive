import { createThumbnailController } from '../renditions/thumbnail-controller';
import { onRuntimeEvent } from '../../api';
import { asRecord } from '../../api/shared';
import { state } from '../../state';

// One row of look-ahead prevents a blank flash during a normal swipe without
// turning the file list's eight-row virtual overscan into network prefetch.
const controller = createThumbnailController({ rootMargin: '72px 0px' });

export const setFileThumbnailRoot = controller.setRoot;
export const teardownFileThumbnails = controller.teardown;
export const setFileThumbnailsActive = controller.setActive;
export const beginFileThumbnailRender = controller.beginRender;
export const registerFileThumbnail = controller.register;
export const unregisterFileThumbnail = controller.unregister;
export const rearmFileThumbnailLocked = controller.rearmLocked;

/** Rearm only mounted rows when sync or an upload publishes a new derivative. */
export function activateFileThumbnailAvailability(): () => void {
    const cleanups = [
        onRuntimeEvent('gallery_rendition_ready', (value) => {
            const payload = asRecord(value);
            const fileId = Number(payload.msg_id);
            if (payload.kind !== 'thumbnail'
                || Number(payload.channel_id) !== Number(state.activeChannel?.id ?? 0)
                || !Number.isSafeInteger(fileId)
                || fileId <= 0) return;
            controller.rearmMissing(fileId);
        }),
        onRuntimeEvent('live_sync_completed', (value) => {
            if (Number(asRecord(value).channel_id) === Number(state.activeChannel?.id ?? 0)) {
                controller.rearmMissing();
            }
        }),
    ];
    return () => {
        for (const cleanup of cleanups) cleanup();
    };
}
