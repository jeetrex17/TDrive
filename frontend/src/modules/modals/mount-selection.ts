import MountSelectionModal from '../../ui/mount/MountSelectionModal.svelte';
import { mountSvelte, type SvelteMountHandle } from '../../ui/mount';

let mountSelectionModalHandle: SvelteMountHandle<Record<string, unknown>> | null = null;

export function setupMountSelectionModal(): void {
    const modal = document.getElementById('mount-selection-modal');
    if (!modal || mountSelectionModalHandle) return;

    modal.replaceChildren();
    mountSelectionModalHandle = mountSvelte(MountSelectionModal, {
        target: modal,
        props: {},
    });
}
