/**
 * What the backup panel says, derived from what the backend reported.
 *
 * The state carries a phase, counts, a message, and two flags that override
 * the phase (a manual pause, a locked vault). Reading all of that in markup
 * turned into a wall of conditions with three competing definitions of
 * "paused". This is the one place that ranks them, so the panel only renders
 * and the activity row and tests can share the same reading.
 */

import type { PhotoBackupState, PhotoBackupStatus } from '../../api/photo-backup';

export type BackupTone = 'idle' | 'busy' | 'success' | 'warning' | 'locked' | 'danger';

export type BackupAction = 'start' | 'pause' | 'resume' | 'retry';

export interface BackupSituation {
    tone: BackupTone;
    /** One short line, the thing a glance should take away. */
    title: string;
    /** The sentence under it; empty when the title says it all. */
    body: string;
    /**
     * The backend's own words about a failure, for the reader who wants them.
     *
     * Kept out of `body` on purpose. What the backend reports is a cause, not a
     * sentence -- an upload path and an errno, which told one user their photos
     * had stopped backing up by filling half the panel with
     * "/data/user/0/.../image-1000219416/.england-london-bridge (2).jpg.partial:
     * open failed: ENOENT". The panel says what happened and what to do; this
     * sits behind a disclosure for whoever is diagnosing it.
     */
    detail: string;
    /** The one thing to do about it, if anything. */
    primary: BackupAction | null;
    /** A less urgent second option, shown smaller. */
    secondary: BackupAction | null;
    /** True while a transfer or scan is under way and the bar should move. */
    progress: boolean;
}

/** Labels per action; the locked variants say what the tap will do first. */
export function actionLabel(action: BackupAction, locked: boolean): string {
    switch (action) {
        case 'start': return locked ? 'Unlock and back up' : 'Back up now';
        case 'resume': return locked ? 'Unlock and resume' : 'Resume';
        case 'retry': return locked ? 'Unlock and retry' : 'Retry';
        case 'pause': return 'Pause';
    }
}

/** "24 backed up · 3 waiting · 1 failed", leaving out whatever is zero. */
export function summaryLine(status: PhotoBackupStatus): string {
    const parts: string[] = [];
    const waiting = status.pending + status.uploading;
    if (status.complete > 0) parts.push(`${status.complete} backed up`);
    if (waiting > 0) parts.push(`${waiting} waiting`);
    if (status.failed > 0) parts.push(`${status.failed} failed`);
    if (status.paused > 0) parts.push(`${status.paused} interrupted`);
    return parts.join(' · ');
}

/**
 * How far through the queue the backup is, counted in files, or null when
 * there is nothing yet to measure.
 *
 * Files, not bytes: the backend does not total the queue's bytes, so a bar
 * wired to them sits at zero for the whole run. A barber pole says only that
 * something is happening, which the spinner beside it already said.
 */
export function queueProgress(status: PhotoBackupStatus): { value: number; max: number } | null {
    const max = status.complete + status.pending + status.uploading + status.failed + status.paused;
    return max > 0 ? { value: status.complete, max } : null;
}

/** The file name alone; sources hand over paths and the row is one line tall. */
export function currentFileName(status: PhotoBackupStatus): string {
    const parts = status.currentFile.split(/[\\/]/).filter(Boolean);
    return parts[parts.length - 1] ?? '';
}

export function describeBackup(state: PhotoBackupState): BackupSituation {
    const { status } = state;
    const waiting = status.pending + status.uploading;
    const hasSources = state.sources.some((source) => source.enabled);

    if (!state.settings.enabled) {
        return { tone: 'idle', title: 'Backup is off', body: 'Turn it on to keep a copy of your photos and videos in this drive.', detail: '', primary: null, secondary: null, progress: false };
    }
    if (state.encryptionRequired) {
        // The queue's own phase decides what the unlock leads into.
        const next: BackupAction = state.manualPaused ? 'resume' : status.failed > 0 || status.paused > 0 ? 'retry' : 'start';
        return { tone: 'locked', title: 'Unlock to continue', body: 'Backups are encrypted. Enter your password and they carry on from where they stopped.', detail: '', primary: next, secondary: null, progress: false };
    }
    // Bodies never carry counts: the summary line under them does, once.
    if (state.manualPaused) {
        return { tone: 'idle', title: 'Paused', body: waiting > 0 ? 'Nothing is sent until you resume.' : 'Nothing is waiting right now.', detail: '', primary: 'resume', secondary: null, progress: false };
    }
    switch (status.phase) {
        case 'uploading':
            return { tone: 'busy', title: 'Backing up', body: currentFileName(status) || 'Preparing the next item…', detail: '', primary: null, secondary: 'pause', progress: true };
        case 'scanning':
            return { tone: 'busy', title: 'Looking for new photos and videos', body: '', detail: '', primary: null, secondary: 'pause', progress: true };
        case 'failed':
            return {
                tone: 'danger',
                title: status.failed === 1 ? '1 item could not be backed up' : `${status.failed} items could not be backed up`,
                body: 'The rest of your library is unaffected. Retry, or leave it and backup will try again on its own.',
                detail: status.message,
                primary: 'retry', secondary: null, progress: false,
            };
        case 'paused':
            // Not the user's pause -- that returned above. Either a device
            // condition the backend named, or items whose last upload was
            // cut off with the remote outcome unknown.
            if (status.paused > 0) {
                return { tone: 'warning', title: status.paused === 1 ? '1 item was interrupted' : `${status.paused} items were interrupted`, body: 'Their last upload did not finish. Retrying may create a duplicate of one that did.', detail: '', primary: 'retry', secondary: null, progress: false };
            }
            return { tone: 'warning', title: 'Waiting', body: status.message || 'Backup will continue when the device allows it.', detail: '', primary: 'start', secondary: null, progress: false };
        case 'queued':
            return { tone: 'idle', title: 'Ready to back up', body: '', detail: '', primary: 'start', secondary: null, progress: false };
        case 'complete':
            return { tone: 'success', title: 'Up to date', body: '', detail: '', primary: 'start', secondary: null, progress: false };
        default:
            if (!hasSources) {
                return { tone: 'idle', title: 'Choose what to back up', body: 'Add a folder or a photo library below to get started.', detail: '', primary: null, secondary: null, progress: false };
            }
            return { tone: 'idle', title: 'Ready', body: status.complete > 0 ? '' : 'Nothing has been backed up yet.', detail: '', primary: 'start', secondary: null, progress: false };
    }
}

/**
 * Where backups land, in the words the drive would use.
 *
 * The backend builds the destination as "Photo backup / <device> / <source>",
 * and the device segment carries a hex suffix so two phones of the same make
 * cannot collide in one drive. That suffix is for the folder, not the reader --
 * "Android device (3c48be52)" tells a person nothing their own phone does not
 * already tell them -- so it is dropped here and kept in the drive.
 */
export function destinationLabel(title: string): string {
    return title
        .split(' / ')
        .map((segment) => segment.replace(/\s*\([0-9a-f]{6,}\)$/i, ''))
        .join(' / ');
}

/** Whether the primary action can be taken right now. */
export function canStart(state: PhotoBackupState): boolean {
    const { settings } = state;
    return settings.enabled && (settings.photos || settings.videos) && state.sources.some((source) => source.enabled);
}
