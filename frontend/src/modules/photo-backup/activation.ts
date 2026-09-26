import type { Readable } from 'svelte/store';
import type { AppView } from '../../ui/app/app-store';

export function activatePhotoBackupOnDashboard(
    view: Readable<AppView>,
    activate: () => () => void,
): () => void {
    let stopBackup: (() => void) | null = null;
    const unsubscribe = view.subscribe((current) => {
        if (current.kind === 'dashboard') {
            if (!stopBackup) stopBackup = activate();
            return;
        }
        stopBackup?.();
        stopBackup = null;
    });

    return () => {
        unsubscribe();
        stopBackup?.();
        stopBackup = null;
    };
}
