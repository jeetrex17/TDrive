// "How would you like to upload?" — the single decision point for
// per-batch encryption. Resolves to { encrypt: boolean } on Continue,
// or null on Cancel.

import { uploadOptionsModal } from '../../ui/modals/upload-options-modal-store';

type UploadChoice = { encrypt: boolean } | null;

let pending: ((result: UploadChoice) => void) | null = null;

function finish(result: UploadChoice): void {
    uploadOptionsModal.close();
    if (pending) {
        const resolve = pending;
        pending = null;
        resolve(result);
    }
}
export function cancelUploadOptions(): void {
    finish(null);
}

export function confirmUploadOptions(choice: { encrypt: boolean }): void {
    finish(choice);
}



export function openUploadOptionsModal({ count }: { count: any }): Promise<UploadChoice> {
    return new Promise<UploadChoice>((resolve) => {
        // A second open while one is pending keeps the visible modal and lets
        // both callers observe the same eventual choice.
        if (pending) {
            const prev = pending;
            pending = (result) => {
                prev(result);
                resolve(result);
            };
            return;
        }
        pending = resolve;
        uploadOptionsModal.open({ count: Number(count) || 0 });
    });
}
