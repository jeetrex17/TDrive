// The trash rendered as the drive.
//
// A trashed item is the same item it was an hour ago, so it is shown the way it
// was shown then: the real file list, the real thumbnails, the same columns and
// the same sort. Only two things differ, and both are deliberate -- a row's
// actions are Restore and Delete forever rather than Open and Download, and the
// meta column carries the countdown, because how long is left is the one fact a
// trash exists to tell you.
//
// Rows are built with the drive's own builders (buildFileRow/buildFolderRow)
// rather than a parallel set. A second builder would be a second definition of
// what a row is, free to drift from the first.
import { state } from '../../state';
import type { TrashEntry } from '../../api/trash';
import { buildFileRow, buildFolderRow, fileThumbnailIdentity, renderFileListRows, renderFileState } from '../file-list';
import { askPurgeTrashEntry, loadTrash, restoreEntry, trashEntries } from './controller';
import { entryOrigin, purgeCountdown } from '../../ui/trash/trash-view';
import type { FileListAction, FileListFileRow, FolderListRow } from '../../ui/file-list/types';
import { setFileThumbnailsActive } from '../../ui/file-list/file-thumbnail-controller';
import { fileListColumnMode } from '../../ui/file-list/column-mode-store';
import { get } from 'svelte/store';

/** "f:2615" -> 2615. Anything else is a folder id and has no message. */
function fileMsgId(objectId: string): number {
    if (!objectId.startsWith('f:')) return 0;
    const parsed = Number(objectId.slice(2));
    return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : 0;
}

/**
 * Restore and Delete forever, in that order: the recoverable action comes
 * first, and the irreversible one sits furthest from the row's own click
 * target. Delete forever only ever asks -- it never acts on the button.
 */
function trashActions(entry: TrashEntry): FileListAction[] {
    return [
        {
            kind: 'restore',
            className: 'row-restore',
            title: `Restore ${entry.name}`,
            label: `Restore ${entry.name}`,
            onClick: (event) => {
                event.stopPropagation();
                void restoreEntry(entry.objectId);
            },
        },
        {
            kind: 'purge',
            className: 'row-purge',
            title: `Delete ${entry.name} forever`,
            label: `Delete ${entry.name} forever`,
            onClick: (event) => {
                event.stopPropagation();
                askPurgeTrashEntry(entry);
            },
        },
    ];
}

/**
 * One trash entry as a file list row.
 *
 * The countdown takes the meta column because it is what the user came here to
 * read; where the item came from goes in the aria label, which is the only
 * place a screen reader would otherwise lose it.
 */
export function trashRow(entry: TrashEntry, now: number, channelId: number): FolderListRow | FileListFileRow {
    const countdown = purgeCountdown(entry.purgeAfter, now);
    const origin = entryOrigin(entry);
    const shared = {
        key: `trash:${entry.objectId}`,
        selectionKey: `trash:${entry.objectId}`,
        parentId: '',
        channelId,
        metaLabel: countdown.label,
        // The phone composes its own one-line "type · size · time"; this is the
        // time part, so both shells report the countdown rather than one of them
        // quietly falling back to the file's age.
        timeLabel: countdown.label,
        actions: trashActions(entry),
        // Restore and Delete forever belong on the row on every shell. The
        // phone's overflow sheet and its Move swipe are built for a live item
        // and have nothing to offer a deleted one.
        actionsInline: true,
    };
    // The date column is what the list sorts on, and in the trash that column
    // shows the countdown -- so the value it sorts by has to be the deadline,
    // not the day the file was originally uploaded. Sorting by a number the
    // column is not showing is how a list ends up looking shuffled.
    //
    // The drive's default sort is date descending, and the newest deletion has
    // the most time left, so the trash opens on the thing the user just lost
    // without needing a sort of its own.
    // Seconds, because that is the unit every other row's date carries and the
    // list's relative-time helpers assume.
    const sortTime = Math.floor(entry.purgeAfter / 1000);
    if (entry.kind === 'folder') {
        return buildFolderRow({ id: entry.objectId, name: entry.name }, '', {
            ...shared,
            modifiedTime: sortTime,
            sizeLabel: '—',
            ariaLabel: `Folder: ${entry.name}, from ${origin}, ${countdown.label}`,
        });
    }
    const msgId = fileMsgId(entry.objectId);
    return buildFileRow({ id: entry.objectId, name: entry.name, size: entry.size }, '', {
        ...shared,
        id: entry.objectId,
        size: entry.size,
        uploadTime: sortTime,
        encrypted: entry.encrypted,
        // Nothing in the trash can be renamed or deleted in the drive's sense;
        // its two real actions are the ones above.
        canDelete: false,
        canRename: false,
        // The live revision, not the entry's remembered one: see api/trash.ts.
        thumbnail: fileThumbnailIdentity(
            { msgId, name: entry.name, revision: entry.revision },
            channelId,
        ),
        // A trashed file has no uploader chip. The chip answers "who put this
        // here", which is not the question a trash row is asked.
        uploaderChip: null,
        ariaLabel: `File: ${entry.name}, from ${origin}, ${countdown.label}`,
    });
}

/**
 * Publishes the current trash into the file list.
 *
 * The clock is read once per render for the same reason the dialog read it once
 * per opening: two rows deleted in the same instant must not disagree about how
 * long they have left.
 */
export function renderTrashRows(): void {
    const list = document.getElementById('file-list');
    if (!list) return;
    const entries = get(trashEntries);
    if (entries.length === 0) {
        renderFileState(
            list,
            'empty',
            'Nothing in the trash',
            'Anything you delete waits here until its time runs out, so you can put it back.',
        );
        return;
    }
    const now = Date.now();
    const channelId = Number(state.activeChannel?.id ?? 0);
    renderFileListRows(list, entries.map((entry) => trashRow(entry, now, channelId)));
}

/**
 * The trash could not be read. It is the file list's own error state, with a
 * retry, so a failure here looks like a failure anywhere else in the drive.
 */
export function renderTrashError(message: string): void {
    const list = document.getElementById('file-list');
    if (!list) return;
    renderFileState(list, 'error', 'The trash could not be opened', message, {
        label: 'Retry',
        onClick: () => void loadTrash(),
    });
}

/**
 * Marks the shell as being in the trash. Thumbnails stay on -- unlike Photos,
 * which owns its own renderer, the trash is the file list and wants the file
 * list's pictures.
 */
export function setTrashMode(on: boolean): void {
    setFileThumbnailsActive(true);
    // The second column stops being the date a file arrived and becomes how
    // long it has left, which is the fact the trash exists to report.
    fileListColumnMode.set(on ? 'purge' : 'date');
    document.querySelector('.main-content')?.classList.toggle('trash-mode', on);
    const nav = document.getElementById('nav-trash');
    nav?.classList.toggle('active', on);
    if (on) nav?.setAttribute('aria-current', 'page');
    else nav?.removeAttribute('aria-current');
}
