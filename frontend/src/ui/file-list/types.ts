export type FileListActionKind = 'open' | 'play' | 'download';

export type FileListAction = {
    kind: FileListActionKind;
    className: string;
    title: string;
    label: string;
    onClick?: (event: MouseEvent, row: FileListRow) => void;
};

type BaseInteractiveRow = {
    key: string;
    selectionKey: string;
    id: string;
    name: string;
    parentId: string;
    metaLabel: string;
    sizeLabel: string;
    ariaLabel: string;
    onClick?: (event: MouseEvent, row: FileListRow) => void;
    onDoubleClick?: (event: MouseEvent, row: FileListRow) => void;
};

export type FolderListRow = BaseInteractiveRow & {
    kind: 'folder';
    actions: FileListAction[];
};

export type FileListFileRow = BaseInteractiveRow & {
    kind: 'file';
    baseName: string;
    ext: string;
    source: string;
    size: number;
    uploaderID: number;
    uploadTime: number;
    encrypted: boolean;
    canDelete: boolean;
    canRename: boolean;
    actions: FileListAction[];
};

export type PendingFolderListRow = {
    kind: 'pending-folder';
    key: string;
    tempId: string;
    name: string;
};

export type FileListRow = FolderListRow | FileListFileRow | PendingFolderListRow;

export type FileListStateView = {
    kind: 'state';
    stateKind: 'loading' | 'empty' | 'error';
    title: string;
    body?: string;
    actionLabel?: string;
    onAction?: () => void;
};

export type FileListRowsView = {
    kind: 'rows';
    rows: FileListRow[];
};

export type FileListView = FileListStateView | FileListRowsView;
