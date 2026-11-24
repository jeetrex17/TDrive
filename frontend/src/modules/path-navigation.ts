// The breadcrumb's path field: goes to a typed or pasted folder path, or to a
// file's folder with the file selected, as Finder's Go to Folder does. Each
// segment is one read of the local drive, so nothing waits on the whole tree.

import { getFileList, getFolderContents } from '../api';
import { state } from '../state';
import { closeChannel } from '../ui/channels/channel-store';
import { appActions } from './app-actions';
import { describeDrivePathFailure, parseDrivePath, resolveDrivePath } from './drive-path';
import { renderBreadcrumb } from './navigation';
import { clearSearch } from './search';

let latestRequest = 0;

function driveLabel(): string {
    return state.activeChannel?.title?.trim() || 'My Drive';
}

/**
 * Opens `text` in the active drive. Resolves to the message to show under the
 * field, or null once it has navigated or given way to a newer request, a
 * drive switch, or a caller that no longer wants it.
 */
export async function goToDrivePath(text: string, stillWanted: () => boolean = () => true): Promise<string | null> {
    const request = ++latestRequest;
    const parsed = parseDrivePath(text);
    if ('kind' in parsed) return describeDrivePathFailure(parsed, driveLabel());

    const driveId = state.activeChannel?.id;
    if (!driveId || state.channelSwitchInProgress) return 'The drive is still opening. Try again in a moment.';
    const current = () => request === latestRequest && stillWanted()
        && !state.channelSwitchInProgress && state.activeChannel?.id === driveId;
    // A walk cut short by another drive opening still owes the field an
    // answer; one the field, a newer path or a passing refresh gave up on
    // does not.
    const abandoned = () => {
        const now = state.activeChannel?.id;
        return request === latestRequest && stillWanted() && now && now !== driveId
            ? 'The drive changed while the path was opening. Try again.'
            : null;
    };

    let target: Awaited<ReturnType<typeof resolveDrivePath>>;
    try {
        target = await resolveDrivePath(parsed, [driveLabel(), 'My Drive'], {
            list: (parentId) => getFolderContents(parentId),
            // The root also lists recent raw Telegram files; a failed read there
            // only means none of them can match.
            rootFiles: () => getFileList().catch(() => []),
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
