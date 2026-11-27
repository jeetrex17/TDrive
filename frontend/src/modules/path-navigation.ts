// The breadcrumb's path field: goes to a typed or pasted folder path, or to a
// file's folder with the file selected, as Finder's Go to Folder does. Each
// segment is one read of the local drive, so nothing waits on the whole tree.

import { getFileList, getFolderContents } from '../api';
import { state } from '../state';
import { closeChannel } from '../ui/channels/channel-store';
import { appActions } from './app-actions';
import { describeDrivePathFailure, parseDrivePath, resolveDrivePath } from './drive-path';
import { folderHistorySteps, interruptFolderHistoryStep } from './folder-history';
import { renderBreadcrumb } from './navigation';
import { clearSearch } from './search';

let latestRequest = 0;

function driveLabel(): string {
    return state.activeChannel?.title?.trim() || 'My Drive';
}

/**
 * Opens `text` in the active drive. Resolves to the message to show under the
 * field, or null once it has navigated or given way to a newer request or a
 * caller that no longer wants it.
 */
export async function goToDrivePath(text: string, stillWanted: () => boolean = () => true): Promise<string | null> {
    const request = ++latestRequest;
    const parsed = parseDrivePath(text);
    if ('kind' in parsed) return describeDrivePathFailure(parsed, driveLabel());

    const driveId = state.activeChannel?.id;
    if (!driveId || state.channelSwitchInProgress) return 'The drive is still opening. Try again in a moment.';
    // A Back asked for before Enter and still reading the folder index is the
    // older choice; it must not land in the middle of the walk.
    interruptFolderHistoryStep();
    // Back, Forward or a click elsewhere while the path is read is the newer
    // choice; the walk must not take the list back afterwards. A history step
    // counts from the moment it is asked for, before its folder is known.
    const startFolder = state.currentFolderId;
    const startSteps = folderHistorySteps();
    const stayed = () => state.currentFolderId === startFolder && state.virtualView === null
        && folderHistorySteps() === startSteps;
    const current = () => request === latestRequest && stillWanted() && stayed()
        && !state.channelSwitchInProgress && state.activeChannel?.id === driveId;
    // A walk the drive cut short (a switch or refresh under way) still owes
    // the field an answer, so the typed path is never dropped silently; one
    // the field, a newer path or another folder gave up on does not.
    const abandoned = () => (request === latestRequest && stillWanted() && stayed()
        ? 'The drive was busy while the path was opening. Try again.'
        : null);

    let target: Awaited<ReturnType<typeof resolveDrivePath>>;
    try {
        target = await resolveDrivePath(parsed, [driveLabel(), 'My Drive'], {
            list: (parentId) => getFolderContents(parentId),
            // The backend answers a failed read of the raw Telegram files with
            // an empty list, so such a file then reads as not found.
            rootFiles: () => getFileList(),
            current,
        });
    } catch (error) {
        if (!current()) return abandoned();
        console.error('Could not open the typed path:', error);
        return 'Could not read the drive. Try again.';
    }
    if (!target || !current()) return abandoned();
    if (target.kind !== 'folder' && target.kind !== 'file') return describeDrivePathFailure(target, driveLabel());

    // The same exits a search result takes: the path replaces whatever the
    // list was showing.
    if (state.searchQuery) clearSearch({ refresh: false });
    closeChannel();
    state.folderPath = target.path.map(({ id, name }) => ({ id, name }));
    state.currentFolderId = target.path[target.path.length - 1]?.id ?? '';
    state.virtualView = null;
    state.pendingFocus = target.kind === 'file' ? { type: 'file', id: target.fileId } : null;
    renderBreadcrumb();
    appActions().refreshFiles();
    return null;
}

/**
 * Called when the path field opens again: a file a previous path named, still
 * waiting for its row, must not take focus from the field the reader is typing in.
 */
export function forgetPendingFileFocus(): void {
    if (state.pendingFocus?.type === 'file') state.pendingFocus = null;
}
