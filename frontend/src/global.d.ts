// Ambient declarations for the globals TDrive wires onto `window`.
//
// The Wails bridge (`go`, `runtime`) is injected at runtime and has no static
// type, so it is `any`. The rest are app-defined entry points assigned in
// main.ts / module setups and called across modules via `window.*`.

export {};

declare global {
    interface Window {
        go?: any;
        runtime?: any;
        refreshFiles: () => void;
        triggerRefresh: () => void | Promise<void>;
        openNewFolderModal: () => void;
        selectFile: () => void;
        initDelete: (id: any, name: any) => void;
        initDeleteFolder: (folderID: any, folderName: any) => void;
        initDownload: (id: any, name: any, size: any) => void;
        initVideoPlayback: (id: any, name: any, size?: any, encrypted?: any) => void;
    }
}
