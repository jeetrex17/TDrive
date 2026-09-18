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
        return { tone: 'idle', title: 'Backup is off', body: 'Turn it on to keep a copy of your photos and videos in this drive.', primary: null, secondary: null, progress: false };
    }
    if (state.encryptionRequired) {
        // The queue's own phase decides what the unlock leads into.
        const next: BackupAction = state.manualPaused ? 'resume' : status.failed > 0 || status.paused > 0 ? 'retry' : 'start';
        return { tone: 'locked', title: 'Unlock to continue', body: 'Backups are encrypted. Enter your password and they carry on from where they stopped.', primary: next, secondary: null, progress: false };
    }
    // Bodies never carry counts: the summary line under them does, once.
    if (state.manualPaused) {
        return { tone: 'idle', title: 'Paused', body: waiting > 0 ? 'Nothing is sent until you resume.' : 'Nothing is waiting right now.', primary: 'resume', secondary: null, progress: false };
    }
    switch (status.phase) {
        case 'uploading':
            return { tone: 'busy', title: 'Backing up', body: currentFileName(status) || 'Preparing the next item…', primary: null, secondary: 'pause', progress: true };
        case 'scanning':
            return { tone: 'busy', title: 'Looking for new photos and videos', body: '', primary: null, secondary: 'pause', progress: true };
        case 'failed':
            return { tone: 'danger', title: status.failed === 1 ? '1 item could not be backed up' : `${status.failed} items could not be backed up`, body: status.message || 'They will be tried again automatically.', primary: 'retry', secondary: null, progress: false };
        case 'paused':
            // Not the user's pause -- that returned above. Either a device
            // condition the backend named, or items whose last upload was
            // cut off with the remote outcome unknown.
            if (status.paused > 0) {
                return { tone: 'warning', title: status.paused === 1 ? '1 item was interrupted' : `${status.paused} items were interrupted`, body: 'Their last upload did not finish. Retrying may create a duplicate of one that did.', primary: 'retry', secondary: null, progress: false };
            }
            return { tone: 'warning', title: 'Waiting', body: status.message || 'Backup will continue when the device allows it.', primary: 'start', secondary: null, progress: false };
        case 'queued':
            return { tone: 'idle', title: 'Ready to back up', body: '', primary: 'start', secondary: null, progress: false };
        case 'complete':
            return { tone: 'success', title: 'Up to date', body: '', primary: 'start', secondary: null, progress: false };
        default:
            if (!hasSources) {
                return { tone: 'idle', title: 'Choose what to back up', body: 'Add a folder or a photo library below to get started.', primary: null, secondary: null, progress: false };
            }
            return { tone: 'idle', title: 'Ready', body: status.complete > 0 ? '' : 'Nothing has been backed up yet.', primary: 'start', secondary: null, progress: false };
    }
}

/** Whether the primary action can be taken right now. */
export function canStart(state: PhotoBackupState): boolean {
    const { settings } = state;
    return settings.enabled && (settings.photos || settings.videos) && state.sources.some((source) => source.enabled);
}
