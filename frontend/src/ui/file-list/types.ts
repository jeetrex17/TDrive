export type FileListActionKind = 'open' | 'play' | 'download' | 'restore' | 'purge';

export type FileListAction = {
    kind: FileListActionKind;
    className: string;
    title: string;
    label: string;
    onClick?: (event: MouseEvent, row: FileListRow) => void;
};

export type FileListUploaderChip = {
    label: string;
    // The phone row shows initials plus a first name instead of the label.
    firstName?: string;
    initials?: string;
};

export type FileThumbnailIdentity = Readonly<{
    channelId: number;
    fileId: number;
    revision: number;
}>;

type BaseInteractiveRow = {
    key: string;
    selectionKey: string;
    id: string;
    name: string;
    parentId: string;
    // Captured when the row is rendered. Actions may be invoked after a drive
    // switch while the old row/action sheet is still on screen.
    channelId?: number;
    metaLabel: string;
    sizeLabel: string;
    ariaLabel: string;
    /**
     * What to say in place of the row's age on the phone's meta line, where
     * type, size and time share one line and the whole line is composed rather
     * than taken from metaLabel. A row that reports something other than "how
     * long ago" -- the trash reports how long is left -- sets this so both
     * shells say the same thing.
     */
    timeLabel?: string;
    /**
     * Renders `actions` on the row itself on the phone as well as the desktop,
     * instead of folding them into the overflow menu and offering a swipe.
     *
     * For rows whose actions are few, important, and have no menu behind them:
     * the phone's overflow sheet is built from the operations a live item
     * supports, so a row that supports none of them would open an empty sheet
     * and a swipe would reveal a Move that cannot happen.
     */
    actionsInline?: boolean;
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
    source: FileSource;
    size: number;
    uploaderID: number;
    uploadTime: number;
    encrypted: boolean;
    canDelete: boolean;
    canRename: boolean;
    // Present only for projected images in the folder currently being viewed.
    // Search and raw Telegram rows intentionally remain icon-only.
    thumbnail?: FileThumbnailIdentity;
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
    secondaryActionLabel?: string;
    onSecondaryAction?: () => void;
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
