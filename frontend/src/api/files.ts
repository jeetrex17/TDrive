import {
    CancelDownload as rawCancelDownload,
    CancelUpload as rawCancelUpload,
    CancelUploadByID as rawCancelUploadById,
    ChangeEncryptionPassword as rawChangeEncryptionPassword,
    CreateEncryptionPassword as rawCreateEncryptionPassword,
    CreateFolder as rawCreateFolder,
    DeleteFile as rawDeleteFile,
    DeleteFolder as rawDeleteFolder,
    DownloadFile as rawDownloadFile,
    DownloadFolder as rawDownloadFolder,
    EncryptionStatus as rawEncryptionStatus,
    GetAllFsMsgIDs as rawGetAllFsMsgIds,
    GetFileList as rawGetFileList,
    GetFolderContents as rawGetFolderContents,
    GetFolderSize as rawGetFolderSize,
    GetFolderStats as rawGetFolderStats,
    GetStorageUsed as rawGetStorageUsed,
    ImportPaths as rawImportPaths,
    MoveFile as rawMoveFile,
    MoveFolder as rawMoveFolder,
    MsgToTdriveSystem as rawMsgToTdriveSystem,
    PlanImport as rawPlanImport,
    PreviewFile as rawPreviewFile,
    PreviewThumbnail as rawPreviewThumbnail,
    RenameFile as rawRenameFile,
    RenameFolder as rawRenameFolder,
    Search as rawSearch,
    SelectFiles as rawSelectFiles,
    SelectFolder as rawSelectFolder,
    SetFileDropEnabled as rawSetFileDropEnabled,
    ShareFile as rawShareFile,
    UploadToDriveFS as rawUploadToDriveFs,
    UseEncryptionPassword as rawUseEncryptionPassword,
} from "../../bindings/TDrive/app";
import type { FileMetaData, Folder, SearchResult } from "../../bindings/TDrive/backend/models";
import type { TDriveFile } from "../../bindings/TDrive/models";
import type {
    DownloadResult,
    EncryptionStatusView,
    FileItem,
    FolderContents,
    FolderItem,
    FolderStat,
    ImportPlan,
    OperationResult,
    PreviewPayload,
    PreviewResult,
    RootFile,
    SearchHit,
    SearchHitType,
    UploadResult,
} from "../types";
import { normalizeOperationResult, requireOperationSuccess } from "./operation";
import { asRecord, nonNegativeNumber } from "./shared";
import { invokeBackend } from "./gateway";

export function normalizeEncryptionStatus(value: unknown): EncryptionStatusView {
    const raw = asRecord(value);
    return {
        available: Boolean(raw.available),
        passwordSet: Boolean(raw.password_set),
        passwordRemembered: Boolean(raw.password_remembered),
        hint: String(raw.hint ?? ""),
    };
}

function normalizeFolderStat(value: unknown): FolderStat {
    const raw = asRecord(value);
    return {
        id: String(raw.id ?? ""),
        bytes: nonNegativeNumber(raw.bytes),
        latestUpload: nonNegativeNumber(raw.latestUpload),
    };
}

export function normalizePreviewPayload(value: unknown): PreviewPayload {
    const raw = asRecord(value);
    return {
        dataBase64: String(raw.data_base64 ?? ""),
        mimeType: String(raw.mime_type ?? ""),
    };
}

export function normalizePreviewResult(value: unknown, fallbackMessage: string): PreviewResult {
    const raw = asRecord(value);
    return {
        result: normalizeOperationResult(raw.result, fallbackMessage),
        payload: normalizePreviewPayload(raw.payload),
    };
}

export function normalizeDownloadResult(value: unknown): DownloadResult {
    const raw = asRecord(value);
    return {
        result: normalizeOperationResult(raw.result, "Download failed"),
        savedPath: String(raw.saved_path ?? ""),
    };
}

export function normalizeImportPlan(value: unknown): ImportPlan {
    const raw = asRecord(value);
    return {
        files: nonNegativeNumber(raw.files),
        folders: nonNegativeNumber(raw.folders),
        bytes: nonNegativeNumber(raw.bytes),
        oversize: nonNegativeNumber(raw.oversize),
        archives: nonNegativeNumber(raw.archives),
        ignored: nonNegativeNumber(raw.ignored),
        maxBytes: nonNegativeNumber(raw.maxBytes),
        maxItems: nonNegativeNumber(raw.maxItems),
        limitExceeded: Boolean(raw.limitExceeded),
        errorCount: nonNegativeNumber(raw.errorCount),
        errors: Array.isArray(raw.errors) ? raw.errors.map((error) => String(error)) : [],
    };
}

export async function createFolder(name: string, parentId: string): Promise<FolderItem> {
    return toFolderItem(await invokeBackend(rawCreateFolder, name, parentId));
}

export async function deleteFile(messageId: number): Promise<OperationResult> {
    return normalizeOperationResult(await invokeBackend(rawDeleteFile, messageId), "Could not delete file");
}

export async function deleteFolder(folderId: string): Promise<OperationResult> {
    return normalizeOperationResult(await invokeBackend(rawDeleteFolder, folderId), "Could not delete folder");
}

export async function getFolderSize(folderId: string): Promise<number> {
    return nonNegativeNumber(await invokeBackend(rawGetFolderSize, folderId));
}

export async function getFolderStats(parentId: string): Promise<FolderStat[]> {
    const stats = await invokeBackend(rawGetFolderStats, parentId);
    return (stats ?? []).map(normalizeFolderStat).filter((entry) => entry.id !== "");
}

export async function getAllFsMsgIds(): Promise<number[]> {
    const ids = await invokeBackend(rawGetAllFsMsgIds);
    return (ids ?? []).filter((id) => Number.isSafeInteger(id) && id > 0);
}

export async function getStorageUsed(): Promise<number> {
    return invokeBackend(rawGetStorageUsed);
}

export async function moveFile(messageId: number, parentId: string): Promise<OperationResult> {
    return normalizeOperationResult(await invokeBackend(rawMoveFile, messageId, parentId), "Could not move file");
}

export async function moveFolder(folderId: string, parentId: string): Promise<OperationResult> {
    return normalizeOperationResult(await invokeBackend(rawMoveFolder, folderId, parentId), "Could not move folder");
}

export async function addTelegramFileToDrive(messageId: number, name: string, size: number, parentId: string): Promise<OperationResult> {
    return normalizeOperationResult(
        await invokeBackend(rawMsgToTdriveSystem, messageId, name, size, parentId),
        "Could not add file to drive",
    );
}

export async function renameFile(messageId: number, name: string): Promise<OperationResult> {
    return normalizeOperationResult(await invokeBackend(rawRenameFile, messageId, name), "Could not rename file");
}

export async function renameFolder(folderId: string, name: string): Promise<OperationResult> {
    return normalizeOperationResult(await invokeBackend(rawRenameFolder, folderId, name), "Could not rename folder");
}

export async function setFileDropEnabled(enabled: boolean): Promise<void> {
    await invokeBackend(rawSetFileDropEnabled, enabled);
}

export async function getEncryptionStatus(): Promise<EncryptionStatusView> {
    return normalizeEncryptionStatus(await invokeBackend(rawEncryptionStatus));
}

export async function createEncryptionPassword(password: string, hint: string): Promise<OperationResult> {
    return normalizeOperationResult(await invokeBackend(rawCreateEncryptionPassword, password, hint), "Could not create encryption password");
}

export async function useEncryptionPassword(password: string): Promise<OperationResult> {
    return normalizeOperationResult(await invokeBackend(rawUseEncryptionPassword, password), "Could not unlock encryption");
}

export async function changeEncryptionPassword(currentPassword: string, newPassword: string, hint: string): Promise<OperationResult> {
    return normalizeOperationResult(
        await invokeBackend(rawChangeEncryptionPassword, currentPassword, newPassword, hint),
        "Could not change encryption password",
    );
}

export async function selectFiles(): Promise<string[]> {
    return (await invokeBackend(rawSelectFiles)) ?? [];
}

export async function selectFolder(): Promise<string> {
    return invokeBackend(rawSelectFolder);
}

export async function downloadFile(channelId: number, messageId: number, accessHash: number, requestId: string): Promise<DownloadResult> {
    return normalizeDownloadResult(await invokeBackend(rawDownloadFile, channelId, messageId, accessHash, requestId));
}

export async function downloadFolder(channelId: number, folderId: string, requestId: string): Promise<DownloadResult> {
    return normalizeDownloadResult(await invokeBackend(rawDownloadFolder, channelId, folderId, requestId));
}

// Opens the OS share sheet for a file TDrive already wrote into its sandbox.
// Mobile only in practice; desktop downloads land wherever the user chose.
export async function shareFile(path: string): Promise<OperationResult> {
    return normalizeOperationResult(await invokeBackend(rawShareFile, path), "Could not open the share sheet");
}

export async function planImport(paths: string[], encrypt: boolean, extract: boolean): Promise<ImportPlan> {
    return normalizeImportPlan(await invokeBackend(rawPlanImport, paths, encrypt, extract));
}

export async function importPaths(paths: string[], parentId: string, encrypt: boolean, extract: boolean): Promise<OperationResult> {
    return normalizeOperationResult(
        await invokeBackend(rawImportPaths, paths, parentId, encrypt, extract),
        "Could not import selected paths",
    );
}

export async function uploadToDriveFs(paths: string[], parentIds: string[], encrypt: boolean): Promise<UploadResult> {
    const raw = asRecord(await invokeBackend(rawUploadToDriveFs, paths, parentIds, encrypt));
    const files = Array.isArray(raw.files) ? raw.files as FileMetaData[] : [];
    return {
        result: normalizeOperationResult(raw.result, "Could not upload selected files"),
        files: files.map(toFileItem),
    };
}

export async function cancelDownload(): Promise<void> {
    await invokeBackend(rawCancelDownload);
}

export async function cancelUpload(): Promise<void> {
    await invokeBackend(rawCancelUpload);
}

// Uploads run several at a time, so stopping one file means naming it. The id
// is the one the backend puts on that upload's progress events.
export async function cancelUploadById(uploadId: number): Promise<void> {
    await invokeBackend(rawCancelUploadById, uploadId);
}

export async function getPreviewFile(messageId: number): Promise<PreviewPayload> {
    const preview = normalizePreviewResult(await invokeBackend(rawPreviewFile, messageId), "Could not preview file");
    requireOperationSuccess(preview.result);
    return preview.payload;
}

export async function getPreviewThumbnail(messageId: number): Promise<PreviewPayload> {
    const preview = normalizePreviewResult(await invokeBackend(rawPreviewThumbnail, messageId), "Could not preview thumbnail");
    requireOperationSuccess(preview.result);
    return preview.payload;
}

export function toFileItem(f: FileMetaData): FileItem {
    return {
        msgId: Number(f.msg_id ?? 0),
        name: String(f.name ?? ""),
        size: Number(f.size ?? 0),
        parentId: String(f.parent_id ?? ""),
        uploadTime: Number(f.upload_time ?? 0),
        uploaderId: Number(f.uploader_id ?? 0),
        encrypted: Boolean(f.encrypted),
        plaintextSize: Number(f.plaintext_size ?? 0),
    };
}

export function toFolderItem(d: Folder): FolderItem {
    return {
        id: String(d.id ?? ""),
        name: String(d.name ?? ""),
        parentId: String(d.parent_id ?? ""),
    };
}

export function toRootFile(f: TDriveFile): RootFile {
    return {
        msgId: Number(f.id ?? 0),
        name: String(f.name ?? ""),
        size: Number(f.size ?? 0),
        accessHash: Number(f.access_hash ?? 0),
        date: Number(f.date ?? 0),
    };
}

export function toSearchHit(h: SearchResult): SearchHit {
    const type: SearchHitType = h.type === "folder" ? "folder" : "file";
    return {
        type,
        id: String(h.id ?? ""),
        name: String(h.name ?? ""),
        parentId: String(h.parent_id ?? ""),
        size: Number(h.size ?? 0),
        uploadTime: Number(h.upload_time ?? 0),
        uploaderId: Number(h.uploader_id ?? 0),
        encrypted: Boolean(h.encrypted),
        plaintextSize: Number(h.plaintext_size ?? 0),
        path: String(h.path ?? ""),
        source: "fs",
    };
}

/** Subfolders and files under a parent folder, normalized. */
export async function getFolderContents(parentId: string): Promise<FolderContents> {
    const fs = await invokeBackend(rawGetFolderContents, parentId);
    return {
        folders: (fs?.folders ?? []).map(toFolderItem),
        files: (fs?.files ?? []).map(toFileItem),
    };
}

/** Flat list of root files read straight from Telegram history, normalized. */
export async function getFileList(): Promise<RootFile[]> {
    const files = await invokeBackend(rawGetFileList);
    return (files ?? []).map(toRootFile);
}

/** Search files and folders in the active drive, normalized. */
export async function search(query: string, limit: number): Promise<SearchHit[]> {
    const hits = await invokeBackend(rawSearch, query, limit);
    return (hits ?? []).map(toSearchHit);
}
