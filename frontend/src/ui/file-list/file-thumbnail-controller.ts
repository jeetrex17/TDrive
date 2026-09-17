import { createThumbnailController } from '../renditions/thumbnail-controller';

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
