// Public frontend boundary over generated Wails bindings.
// Domain implementations stay internal; UI modules import only from this file.
export * from "./api/drives";
export * from "./api/files";
export * from "./api/media";
export * from "./api/mount";
export * from "./api/operation";
export * from "./api/runtime";
export * from "./api/session";
export * from "./api/updates";

export type {
    DownloadResult,
    OperationError,
    OperationErrorCode,
    OperationResult,
    PreviewResult,
    UploadResult,
} from "./types";
