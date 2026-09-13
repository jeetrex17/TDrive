export type FileListActionKind = 'open' | 'play' | 'download';

export type FileListAction = {
    kind: FileListActionKind;
    className: string;
    title: string;
    label: string;
    onClick?: (event: MouseEvent, row: FileListRow) => void;
};

export type FileListUploaderChip = {
    label: string;
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
    // Subtree stats, 0 until the async lookup resolves (or when it fails);
    // sizeLabel/metaLabel are their display forms. modifiedTime is the latest
    // file upload in the subtree — the closest available stand-in for a true
    // modification time until dirents carry one.
    size: number;
    modifiedTime: number;
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
    uploaderChip?: FileListUploaderChip | null;
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

export type FileSource = 'fs' | 'tg';

type CommandItemBase = {
    name: string;
    parentId?: string;
    row?: HTMLElement;
    canDelete?: boolean;
    canRename?: boolean;
};

export type FolderCommandItem = CommandItemBase & {
    type: 'folder';
    id: string;
};

export type ProjectedFileCommandItem = CommandItemBase & {
    type: 'file';
    id: number;
    size?: number;
    source?: 'fs';
    uploaderID?: number;
};

export type TelegramFileCommandItem = CommandItemBase & {
    type: 'file';
    id: number;
    size: number;
    source: 'tg';
    parentId: string;
    uploaderID?: number;
};

export type FileCommandItem = FolderCommandItem | ProjectedFileCommandItem | TelegramFileCommandItem;

export type BulkFileCommandTarget = {
    type: 'bulk';
    items: FileCommandItem[];
    parentId: string;
};

export type FileCommandTarget = FileCommandItem | BulkFileCommandTarget;

export type FileDragState = {
    items: FileCommandItem[];
    parentId: string;
    blocked: Set<string>;
    row: HTMLElement;
};
