import type { FileListFileRow, FileListRow, FolderListRow, PendingFolderListRow } from './types';

export type FileSortKey = 'name' | 'date' | 'size';
export type FileSortDirection = 'asc' | 'desc';

export type FileSortState = {
    key: FileSortKey;
    direction: FileSortDirection;
};

export const DEFAULT_FILE_SORT: FileSortState = Object.freeze({
    key: 'date',
    direction: 'desc',
});

const DEFAULT_DIRECTIONS: Record<FileSortKey, FileSortDirection> = {
    name: 'asc',
    date: 'desc',
    size: 'desc',
};

const collator = new Intl.Collator(undefined, {
    numeric: true,
    sensitivity: 'base',
});

export function nextFileSortState(current: FileSortState, key: FileSortKey): FileSortState {
    if (current.key === key) {
        return {
            key,
            direction: current.direction === 'asc' ? 'desc' : 'asc',
        };
    }
    return { key, direction: DEFAULT_DIRECTIONS[key] };
}

export function sortFileListRows(rows: readonly FileListRow[], sort: FileSortState): FileListRow[] {
    const pending: PendingFolderListRow[] = [];
    const folders: FolderListRow[] = [];
    const files: FileListFileRow[] = [];

    for (const row of rows) {
        if (row.kind === 'pending-folder') pending.push(row);
        else if (row.kind === 'folder') folders.push(row);
        else files.push(row);
    }

    const folderDirection = sort.key === 'name' ? sort.direction : 'asc';
    return [
        ...pending,
        ...folders.sort((a, b) => compareDirection(compareName(a, b), folderDirection)),
        ...files.sort((a, b) => compareFiles(a, b, sort)),
    ];
}

function compareFiles(a: FileListFileRow, b: FileListFileRow, sort: FileSortState): number {
    const primary = compareFilePrimary(a, b, sort.key);
    if (primary !== 0) return compareDirection(primary, sort.direction);
    return compareFileTieBreak(a, b);
}

function compareFilePrimary(a: FileListFileRow, b: FileListFileRow, key: FileSortKey): number {
    switch (key) {
        case 'date':
            return compareNumber(a.uploadTime, b.uploadTime);
        case 'size':
            return compareNumber(a.size, b.size);
        case 'name':
        default:
            return compareName(a, b);
    }
}

function compareFileTieBreak(a: FileListFileRow, b: FileListFileRow): number {
    const name = compareName(a, b);
    if (name !== 0) return name;
    return collator.compare(a.key, b.key);
}

function compareName(a: Pick<FileListRow, 'name'>, b: Pick<FileListRow, 'name'>): number {
    return collator.compare(a.name || '', b.name || '');
}

function compareNumber(a: number, b: number): number {
    const left = Number.isFinite(a) ? a : 0;
    const right = Number.isFinite(b) ? b : 0;
    return left - right;
}

function compareDirection(value: number, direction: FileSortDirection): number {
    return direction === 'asc' ? value : -value;
}
