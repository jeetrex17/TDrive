// Gallery adapter around the shared viewport thumbnail controller. Keeping the
// public functions stable lets the gallery and viewer share cached placeholders
// while Files owns an independent scroll root.
import {
    createThumbnailController,
    retryDelay,
    type ThumbnailPatch,
    type ThumbnailRegistration,
    type ThumbnailStatus,
} from '../renditions/thumbnail-controller';

export type CellStatus = ThumbnailStatus;
export type CellPatch = ThumbnailPatch;
export interface CellRegistration {
    msgId: number;
    revision?: number;
    apply: ThumbnailRegistration['apply'];
}

const controller = createThumbnailController({ rootMargin: '160px 0px' });

export const setRoot = controller.setRoot;
export const teardown = controller.teardown;
export const setActive = controller.setActive;
export const beginRender = controller.beginRender;
export const rearmLocked = controller.rearmLocked;
export { retryDelay };

export function registerCell(node: HTMLElement, registration: CellRegistration): void {
    controller.register(node, {
        fileId: registration.msgId,
        revision: registration.revision,
        apply: registration.apply,
    });
}

export function unregisterCell(node: HTMLElement): void {
    controller.unregister(node);
}

export function cachedThumb(channelId: number, msgId: number): string {
    return controller.cached(channelId, msgId);
}
