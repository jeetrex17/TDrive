// Typed boundary over the generated Wails bindings.
//
// Every read that returns drive data goes through here so the snake_case Go
// payloads are normalized into the camelCase types in `types.ts` exactly once.
// UI modules should import from this module instead of calling the raw
// `wailsjs/go/main/App` functions directly.

import {
    AppVersion as rawAppVersion,
    ApproveJoinRequest as rawApproveJoinRequest,
    AttachNativeMedia as rawAttachNativeMedia,
    CancelDownload as rawCancelDownload,
    CancelUpdateDownload as rawCancelUpdateDownload,
    CancelUpload as rawCancelUpload,
    ChangeEncryptionPassword as rawChangeEncryptionPassword,
    CheckForUpdate as rawCheckForUpdate,
    CheckLoginStatus as rawCheckLoginStatus,
    CheckPendingJoin as rawCheckPendingJoin,
    CheckSystemStatus as rawCheckSystemStatus,
    CloseMedia as rawCloseMedia,
    CloseNativeMedia as rawCloseNativeMedia,
    CreateEncryptionPassword as rawCreateEncryptionPassword,
    CreateFolder as rawCreateFolder,
    CreatePersonalDrive as rawCreatePersonalDrive,
    CreateSharedDrive as rawCreateSharedDrive,
    DeleteFile as rawDeleteFile,
    DeleteFolder as rawDeleteFolder,
    DiscoverPersonalDrives as rawDiscoverPersonalDrives,
    DownloadFile as rawDownloadFile,
    DownloadFolder as rawDownloadFolder,
    DownloadUpdate as rawDownloadUpdate,
    EncryptionStatus as rawEncryptionStatus,
    GetAllFsMsgIDs as rawGetAllFsMsgIds,
    GetApprovalInviteLink as rawGetApprovalInviteLink,
    GetFileList as rawGetFileList,
    GetFolderContents as rawGetFolderContents,
    GetFolderSize as rawGetFolderSize,
    GetFolderStats as rawGetFolderStats,
    GetInviteLink as rawGetInviteLink,
    GetMediaStats as rawGetMediaStats,
    GetStorageUsed as rawGetStorageUsed,
    GetUpdateState as rawGetUpdateState,
    HideNativeSeekThumbnail as rawHideNativeSeekThumbnail,
    ImportPaths as rawImportPaths,
    InstallUpdateAndRestart as rawInstallUpdateAndRestart,
    JoinSharedDrive as rawJoinSharedDrive,
    LeaveSharedDrive as rawLeaveSharedDrive,
    ListChannels as rawListChannels,
    ListJoinRequests as rawListJoinRequests,
    ListMedia as rawListMedia,
    ListPendingJoins as rawListPendingJoins,
    LoginPhoneNumber as rawLoginPhoneNumber,
    Logout as rawLogout,
    Me as rawMe,
    MountDrive as rawMountDrive,
    MountDrives as rawMountDrives,
    MountStatus as rawMountStatus,
    MoveFile as rawMoveFile,
    MoveFolder as rawMoveFolder,
    MoveNativeSeekThumbnail as rawMoveNativeSeekThumbnail,
    MsgToTdriveSystem as rawMsgToTdriveSystem,
    MyUserID as rawMyUserId,
    NativeMediaCommand as rawNativeMediaCommand,
    OpenMedia as rawOpenMedia,
    OpenNativeMedia as rawOpenNativeMedia,
    OpenStream as rawOpenStream,
    OpenUpdatePage as rawOpenUpdatePage,
    PlanImport as rawPlanImport,
    PreparePersonalDrive as rawPreparePersonalDrive,
    PreviewFile as rawPreviewFile,
    PreviewThumbnail as rawPreviewThumbnail,
    RejectJoinRequest as rawRejectJoinRequest,
    RemovePendingJoin as rawRemovePendingJoin,
    RenameFile as rawRenameFile,
    RenameFolder as rawRenameFolder,
    ResizeNativeMedia as rawResizeNativeMedia,
    ResolveUsernames as rawResolveUsernames,
    SaveSetup as rawSaveSetup,
    Search as rawSearch,
    SelectFiles as rawSelectFiles,
    SelectFolder as rawSelectFolder,
    SelectPersonalDrive as rawSelectPersonalDrive,
    SetActiveChannel as rawSetActiveChannel,
    SetFileDropEnabled as rawSetFileDropEnabled,
    ShowNativeSeekThumbnail as rawShowNativeSeekThumbnail,
    SumbitCode as rawSubmitCode,
    SumbitPassword as rawSubmitPassword,
    SyncChannel as rawSyncChannel,
    Thumbnail as rawThumbnail,
    UnmountDrive as rawUnmountDrive,
    UpdateMediaPlayback as rawUpdateMediaPlayback,
    UploadToDriveFS as rawUploadToDriveFs,
    UseEncryptionPassword as rawUseEncryptionPassword,
} from "../wailsjs/go/main/App";
import {
    BrowserOpenURL as rawBrowserOpenUrl,
    OnFileDrop as rawOnFileDrop,
    WindowFullscreen as rawWindowFullscreen,
    WindowIsFullscreen as rawWindowIsFullscreen,
    WindowSetDarkTheme as rawWindowSetDarkTheme,
    WindowSetLightTheme as rawWindowSetLightTheme,
    WindowSetSystemDefaultTheme as rawWindowSetSystemDefaultTheme,
    WindowUnfullscreen as rawWindowUnfullscreen,
} from "../wailsjs/runtime/runtime";
import type { backend, main, media } from "../wailsjs/go/models";
import type {
    AppVersion,
    DownloadResult,
    DriveChannel,
    EncryptionStatusView,
    FileItem,
    FolderItem,
    FolderContents,
    FolderStat,
    ImportPlan,
    JoinDriveResult,
    JoinRequest,
    PendingJoin,
    PersonalDriveCandidate,
    PersonalDriveSetup,
    PreviewPayload,
    RootFile,
    SearchHit,
    SearchHitType,
    SelfUser,
    MountedDrive,
    MountedDriveKind,
    MountableDrive,
    MountMode,
    MountPhase,
    MountStatusView,
    MountWriteState,
    UpdateSnapshot,
} from "./types";

export const MOUNT_LABEL = 'Tdrive personal' as const;

type UnknownRecord = Record<string, unknown>;

const UNSAFE_MOUNT_DETAIL = /(?:https?:\/\/|webdav:\/\/|dav:\/\/|tdrive-[a-f\d]{8,}|mount_webdav|\bnet\s+use\b|\bgio\s+mount\b)/i;
const LOOPBACK_LOCATION = /(?:127\.0\.0\.1|\blocalhost\b|\[::1\])/i;

function asRecord(value: unknown): UnknownRecord {
    return value !== null && typeof value === 'object' ? value as UnknownRecord : {};
}

function boundedText(value: unknown, maxLength: number): string {
    if (typeof value !== 'string') return '';
    return [...value]
        .filter((character) => {
            const codePoint = character.codePointAt(0) ?? 0;
            return codePoint > 31 && codePoint !== 127;
        })
        .join('')
        .trim()
        .slice(0, maxLength);
}

function finiteNumber(value: unknown, fallback = 0): number {
    const number = Number(value);
    return Number.isFinite(number) ? number : fallback;
}

function nonNegativeNumber(value: unknown): number {
    return Math.max(0, finiteNumber(value));
}

function normalizeDriveKind(value: unknown): DriveChannel["kind"] {
    return value === "personal" || value === "shared" ? value : "unknown";
}

export function normalizeDriveChannel(value: unknown): DriveChannel {
    const raw = asRecord(value);
    return {
        id: finiteNumber(raw.id),
        title: String(raw.title ?? ""),
        kind: normalizeDriveKind(raw.kind),
        isActive: Boolean(raw.is_active),
        inviteLink: String(raw.invite_link ?? ""),
    };
}

export function normalizePendingJoin(value: unknown): PendingJoin {
    const raw = asRecord(value);
    return {
        inviteHash: String(raw.invite_hash ?? ""),
        inviteLink: String(raw.invite_link ?? ""),
        title: String(raw.title ?? ""),
        requestedAt: finiteNumber(raw.requested_at),
        lastCheckedAt: finiteNumber(raw.last_checked_at),
        status: String(raw.status ?? ""),
        lastError: String(raw.last_error ?? ""),
    };
}

export function normalizeJoinDriveResult(value: unknown): JoinDriveResult {
    const raw = asRecord(value);
    return {
        status: String(raw.status ?? ""),
        channel: raw.channel == null ? null : normalizeDriveChannel(raw.channel),
        pending: raw.pending == null ? null : normalizePendingJoin(raw.pending),
    };
}

export function normalizeJoinRequest(value: unknown): JoinRequest {
    const raw = asRecord(value);
    return {
        userId: finiteNumber(raw.user_id),
        displayName: String(raw.display_name ?? ""),
        username: String(raw.username ?? ""),
        requestedAt: finiteNumber(raw.requested_at),
        about: String(raw.about ?? ""),
    };
}

function normalizePersonalDriveCandidate(value: unknown): PersonalDriveCandidate {
    const raw = asRecord(value);
    return {
        id: String(raw.id ?? ""),
        title: String(raw.title ?? ""),
        createdAt: finiteNumber(raw.created_at),
        hasActivity: Boolean(raw.has_activity),
        recommended: Boolean(raw.recommended),
    };
}

function normalizePersonalDriveSetup(value: unknown): PersonalDriveSetup {
    const raw = asRecord(value);
    return {
        status: String(raw.status ?? ""),
        activeChannelId: String(raw.active_channel_id ?? ""),
    };
}

export function normalizeSelfUser(value: unknown): SelfUser {
    const raw = asRecord(value);
    return {
        userId: finiteNumber(raw.user_id),
        displayName: String(raw.display_name ?? ""),
        username: String(raw.username ?? ""),
        photoBase64: String(raw.photo_base64 ?? ""),
    };
}

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

export function normalizeDownloadResult(value: unknown): DownloadResult {
    const raw = asRecord(value);
    const rawStatus = String(raw.status ?? "error").toLowerCase();
    const status = rawStatus === "success" || rawStatus === "canceled" ? rawStatus : "error";
    return {
        status,
        message: String(raw.message ?? "Download failed"),
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

function normalizeUpdateRelease(value: unknown): UpdateSnapshot["latest"] {
    if (value == null) return null;
    const raw = asRecord(value);
    return {
        version: String(raw.version ?? ""),
        tag: String(raw.tag ?? ""),
        pageUrl: String(raw.page_url ?? ""),
        publishedAt: String(raw.published_at ?? ""),
        assetName: String(raw.asset_name ?? ""),
        assetSize: nonNegativeNumber(raw.asset_size),
    };
}

const UPDATE_PHASES = new Set<UpdateSnapshot["phase"]>([
    "idle",
    "disabled",
    "checking",
    "up_to_date",
    "available",
    "downloading",
    "ready",
    "installing",
    "installed",
]);

export function normalizeUpdateSnapshot(value: unknown): UpdateSnapshot {
    const raw = asRecord(value);
    const phase = String(raw.phase ?? "idle");
    return {
        phase: UPDATE_PHASES.has(phase as UpdateSnapshot["phase"])
            ? phase as UpdateSnapshot["phase"]
            : "idle",
        currentVersion: String(raw.current_version ?? ""),
        latest: normalizeUpdateRelease(raw.latest),
        installable: Boolean(raw.installable),
        installHint: String(raw.install_hint ?? ""),
        downloadedBytes: nonNegativeNumber(raw.downloaded_bytes),
        totalBytes: nonNegativeNumber(raw.total_bytes),
        checkedAt: nonNegativeNumber(raw.checked_at),
        error: String(raw.error ?? ""),
        errorStage: String(raw.error_stage ?? ""),
    };
}

export async function checkSystemStatus(): Promise<string> {
    return rawCheckSystemStatus();
}

export async function saveSetup(apiId: number, apiHash: string): Promise<string> {
    return rawSaveSetup(apiId, apiHash);
}

export async function loginPhoneNumber(phone: string): Promise<void> {
    await rawLoginPhoneNumber(phone);
}

export async function submitCode(code: string): Promise<void> {
    await rawSubmitCode(code);
}

export async function submitPassword(password: string): Promise<void> {
    await rawSubmitPassword(password);
}

export async function checkLoginStatus(): Promise<boolean> {
    return rawCheckLoginStatus();
}

export async function preparePersonalDrive(): Promise<PersonalDriveSetup> {
    return normalizePersonalDriveSetup(await rawPreparePersonalDrive());
}

export async function discoverPersonalDrives(): Promise<PersonalDriveCandidate[]> {
    const drives = await rawDiscoverPersonalDrives();
    return (drives ?? []).map(normalizePersonalDriveCandidate);
}

export async function selectPersonalDrive(channelId: string): Promise<void> {
    await rawSelectPersonalDrive(channelId);
}

export async function createPersonalDrive(): Promise<void> {
    await rawCreatePersonalDrive();
}

export async function getMyUserId(): Promise<number> {
    return rawMyUserId();
}

export async function listChannels(): Promise<DriveChannel[]> {
    const channels = await rawListChannels();
    return (channels ?? []).map(normalizeDriveChannel);
}

export async function createSharedDrive(title: string, requireApproval: boolean): Promise<DriveChannel> {
    return normalizeDriveChannel(await rawCreateSharedDrive(title, requireApproval));
}

export async function joinSharedDrive(inviteLink: string): Promise<JoinDriveResult> {
    return normalizeJoinDriveResult(await rawJoinSharedDrive(inviteLink));
}

export async function getInviteLink(channelId: number): Promise<string> {
    return rawGetInviteLink(channelId);
}

export async function getApprovalInviteLink(channelId: number): Promise<string> {
    return rawGetApprovalInviteLink(channelId);
}

export async function leaveSharedDrive(channelId: number): Promise<void> {
    await rawLeaveSharedDrive(channelId);
}

export async function listPendingJoins(): Promise<PendingJoin[]> {
    const pending = await rawListPendingJoins();
    return (pending ?? []).map(normalizePendingJoin);
}

export async function checkPendingJoin(inviteHash: string): Promise<JoinDriveResult> {
    return normalizeJoinDriveResult(await rawCheckPendingJoin(inviteHash));
}

export async function removePendingJoin(inviteHash: string): Promise<void> {
    await rawRemovePendingJoin(inviteHash);
}

export async function listJoinRequests(channelId: number): Promise<JoinRequest[]> {
    const requests = await rawListJoinRequests(channelId);
    return (requests ?? []).map(normalizeJoinRequest);
}

export async function approveJoinRequest(channelId: number, userId: number): Promise<void> {
    await rawApproveJoinRequest(channelId, userId);
}

export async function rejectJoinRequest(channelId: number, userId: number): Promise<void> {
    await rawRejectJoinRequest(channelId, userId);
}

export async function setActiveChannel(channelId: number): Promise<void> {
    await rawSetActiveChannel(channelId);
}

export async function syncChannel(channelId: number): Promise<void> {
    await rawSyncChannel(channelId);
}

export async function createFolder(name: string, parentId: string): Promise<FolderItem> {
    return toFolderItem(await rawCreateFolder(name, parentId));
}

export async function deleteFile(messageId: number): Promise<string> {
    return rawDeleteFile(messageId);
}

export async function deleteFolder(folderId: string): Promise<string> {
    return rawDeleteFolder(folderId);
}

export async function getFolderSize(folderId: string): Promise<number> {
    return nonNegativeNumber(await rawGetFolderSize(folderId));
}

export async function getFolderStats(parentId: string): Promise<FolderStat[]> {
    const stats = await rawGetFolderStats(parentId);
    return (stats ?? []).map(normalizeFolderStat).filter((entry) => entry.id !== "");
}

export async function getAllFsMsgIds(): Promise<number[]> {
    const ids = await rawGetAllFsMsgIds();
    return (ids ?? []).filter((id) => Number.isSafeInteger(id) && id > 0);
}

export async function getStorageUsed(): Promise<number> {
    return rawGetStorageUsed();
}

export async function moveFile(messageId: number, parentId: string): Promise<string> {
    return rawMoveFile(messageId, parentId);
}

export async function moveFolder(folderId: string, parentId: string): Promise<string> {
    return rawMoveFolder(folderId, parentId);
}

export async function addTelegramFileToDrive(messageId: number, name: string, size: number, parentId: string): Promise<string> {
    return rawMsgToTdriveSystem(messageId, name, size, parentId);
}

export async function renameFile(messageId: number, name: string): Promise<string> {
    return rawRenameFile(messageId, name);
}

export async function renameFolder(folderId: string, name: string): Promise<string> {
    return rawRenameFolder(folderId, name);
}

export async function setFileDropEnabled(enabled: boolean): Promise<void> {
    await rawSetFileDropEnabled(enabled);
}

export async function getEncryptionStatus(): Promise<EncryptionStatusView> {
    return normalizeEncryptionStatus(await rawEncryptionStatus());
}

export async function createEncryptionPassword(password: string, hint: string): Promise<void> {
    await rawCreateEncryptionPassword(password, hint);
}

export async function useEncryptionPassword(password: string): Promise<void> {
    await rawUseEncryptionPassword(password);
}

export async function changeEncryptionPassword(currentPassword: string, newPassword: string, hint: string): Promise<void> {
    await rawChangeEncryptionPassword(currentPassword, newPassword, hint);
}

export async function logout(mode: string): Promise<void> {
    await rawLogout(mode);
}

export async function getSelfUser(): Promise<SelfUser> {
    return normalizeSelfUser(await rawMe());
}

export async function resolveUsernames(userIds: number[]): Promise<Record<string, string>> {
    const resolved = await rawResolveUsernames(userIds);
    return Object.fromEntries(Object.entries(resolved ?? {}).map(([id, name]) => [id, String(name)]));
}

export async function selectFiles(): Promise<string[]> {
    return rawSelectFiles();
}

export async function selectFolder(): Promise<string> {
    return rawSelectFolder();
}

export async function downloadFile(messageId: number, accessHash: number): Promise<DownloadResult> {
    return normalizeDownloadResult(await rawDownloadFile(messageId, accessHash));
}

export async function downloadFolder(folderId: string): Promise<DownloadResult> {
    return normalizeDownloadResult(await rawDownloadFolder(folderId));
}

export async function planImport(paths: string[], encrypt: boolean, extract: boolean): Promise<ImportPlan> {
    return normalizeImportPlan(await rawPlanImport(paths, encrypt, extract));
}

export async function importPaths(paths: string[], parentId: string, encrypt: boolean, extract: boolean): Promise<void> {
    await rawImportPaths(paths, parentId, encrypt, extract);
}

export async function uploadToDriveFs(paths: string[], parentIds: string[], encrypt: boolean): Promise<FileItem[]> {
    const files = await rawUploadToDriveFs(paths, parentIds, encrypt);
    return (files ?? []).map(toFileItem);
}

export async function cancelDownload(): Promise<void> {
    await rawCancelDownload();
}

export async function cancelUpload(): Promise<void> {
    await rawCancelUpload();
}

export async function getPreviewFile(messageId: number): Promise<PreviewPayload> {
    return normalizePreviewPayload(await rawPreviewFile(messageId));
}

export async function getPreviewThumbnail(messageId: number): Promise<PreviewPayload> {
    return normalizePreviewPayload(await rawPreviewThumbnail(messageId));
}

export async function getAppVersion(): Promise<AppVersion> {
    const raw = await rawAppVersion();
    return {
        version: String(raw?.version ?? ""),
        os: String(raw?.os ?? ""),
        arch: String(raw?.arch ?? ""),
        devBuild: Boolean(raw?.dev_build),
    };
}

export async function getUpdateState(): Promise<UpdateSnapshot> {
    return normalizeUpdateSnapshot(await rawGetUpdateState());
}

export async function checkForUpdate(): Promise<UpdateSnapshot> {
    return normalizeUpdateSnapshot(await rawCheckForUpdate());
}

export async function downloadUpdate(): Promise<void> {
    await rawDownloadUpdate();
}

export async function cancelUpdateDownload(): Promise<void> {
    await rawCancelUpdateDownload();
}

export async function installUpdateAndRestart(): Promise<void> {
    await rawInstallUpdateAndRestart();
}

export async function openUpdatePage(): Promise<void> {
    await rawOpenUpdatePage();
}

export function onRuntimeEvent<TArgs extends unknown[]>(eventName: string, callback: (...data: TArgs) => void): (() => void) | null {
    const eventsOn = window.runtime?.EventsOn;
    if (!eventsOn) return null;
    const unsubscribe = eventsOn(eventName, (...data: unknown[]) => callback(...data as TArgs));
    return typeof unsubscribe === "function" ? unsubscribe : null;
}

export function onUpdateState(callback: (snapshot: UpdateSnapshot) => void): (() => void) | null {
    return onRuntimeEvent<[unknown]>("update_state", (payload) => callback(normalizeUpdateSnapshot(payload)));
}

export function onNativeFileDrop(callback: (x: number, y: number, paths: string[]) => void): void {
    try {
        rawOnFileDrop(callback, true);
    } catch {
        // The browser-only development surface has no native drop runtime.
    }
}
export function openExternalUrl(url: string): void {
    try {
        rawBrowserOpenUrl(url);
    } catch {
        window.open(url, "_blank", "noopener,noreferrer");
    }
}

export function fullscreenAvailable(): boolean {
    return Boolean(
        window.runtime?.WindowFullscreen
        && window.runtime?.WindowUnfullscreen
        && window.runtime?.WindowIsFullscreen
    );
}

export function enterFullscreen(): void {
    rawWindowFullscreen();
}

export function exitFullscreen(): void {
    rawWindowUnfullscreen();
}

export async function isFullscreen(): Promise<boolean> {
    return rawWindowIsFullscreen();
}

export function setNativeSystemTheme(): void {
    rawWindowSetSystemDefaultTheme();
}

export function setNativeLightTheme(): void {
    rawWindowSetLightTheme();
}

export function setNativeDarkTheme(): void {
    rawWindowSetDarkTheme();
}
function normalizeMountPhase(value: unknown, mounted: boolean, error: string): MountPhase {
    if (error || value === 'error') return 'error';
    if (value === 'disconnecting') return 'disconnecting';
    if (mounted) return 'mounted';
    return value === 'mounting' ? 'mounting' : 'idle';
}

function normalizeMountedDrive(value: unknown): MountedDrive | null {
    const raw = asRecord(value);
    const rawID = Number(raw.id ?? 0);
    const id = Number.isSafeInteger(rawID) && rawID > 0 ? rawID : 0;
    const title = boundedText(raw.title, 160);
    const rawKind = boundedText(raw.kind, 24);
    const kind: MountedDriveKind = rawKind === 'personal' || rawKind === 'shared'
        ? rawKind
        : 'unknown';
    if (id === 0 && !title && rawKind === '') return null;
    return { id, title, kind };
}

function normalizeMountLocation(value: unknown): string {
    const location = boundedText(value, 320);
    if (UNSAFE_MOUNT_DETAIL.test(location) || LOOPBACK_LOCATION.test(location)) return '';
    return location;
}

function normalizeMountLabel(value: unknown): string {
    const label = boundedText(value, 96);
    if (!label || UNSAFE_MOUNT_DETAIL.test(label) || LOOPBACK_LOCATION.test(label)) return MOUNT_LABEL;
    return label;
}

function normalizeMountMode(value: unknown): MountMode {
    return value === 'read-write' ? 'read-write' : 'read-only';
}

function normalizeMountWriteState(value: unknown, mode: MountMode): MountWriteState {
    if (mode === 'read-only') return 'disabled';
    if (value === 'starting' || value === 'ready' || value === 'draining' || value === 'drained') {
        return value;
    }
    return 'starting';
}

function normalizeActiveWrites(value: unknown, mode: MountMode): number {
    if (mode === 'read-only') return 0;
    const count = Number(value ?? 0);
    return Number.isSafeInteger(count) && count >= 0 && count <= 1024 ? count : 0;
}

/** Converts backend/bridge failures into endpoint-free user-facing text. */
export function safeMountError(value: unknown, fallback = 'The mount operation failed. Try again.'): string {
    const message = value instanceof Error
        ? boundedText(value.message, 240)
        : boundedText(value, 240);
    if (!message || UNSAFE_MOUNT_DETAIL.test(message) || LOOPBACK_LOCATION.test(message)) return fallback;
    return message;
}

/** Normalize the Go DTO and intentionally drop endpoint URLs and command hints. */
export function normalizeMountStatus(value: unknown): MountStatusView {
    const raw = asRecord(value);
    const mounted = Boolean(raw.mounted);
    const error = safeMountError(raw.error, 'The drive could not be mounted. Try again.');
    const hasError = boundedText(raw.error, 1) !== '';
    const mode = normalizeMountMode(raw.mode);
    const writeState = normalizeMountWriteState(raw.write_state, mode);
    return {
        phase: normalizeMountPhase(raw.phase, mounted, hasError ? error : ''),
        mounted,
        mode,
        writeState,
        acceptingWrites: mounted && mode === 'read-write' && writeState === 'ready' && raw.accepting_writes === true,
        activeWrites: normalizeActiveWrites(raw.active_writes, mode),
        label: normalizeMountLabel(raw.label),
        location: normalizeMountLocation(raw.location),
        error: hasError ? error : '',
        drive: normalizeMountedDrive(raw.drive),
    };
}

/** Normalize the channel list before exposing it to the mount picker. */
export function normalizeMountableDrives(value: unknown): MountableDrive[] {
    if (!Array.isArray(value)) return [];

    const seen = new Set<number>();
    const drives = value.flatMap((entry): MountableDrive[] => {
        const raw = asRecord(entry);
        const id = Number(raw.id ?? 0);
        const kind = raw.kind === 'personal' || raw.kind === 'shared' ? raw.kind : null;
        if (!Number.isSafeInteger(id) || id <= 0 || !kind || seen.has(id)) return [];

        seen.add(id);
        return [{
            id,
            title: boundedText(raw.title, 160) || (kind === 'personal' ? 'Personal' : 'Shared drive'),
            kind,
        }];
    });

    return [...drives].sort((left, right) => {
        if (left.kind === right.kind) return 0;
        return left.kind === 'personal' ? -1 : 1;
    });
}

export async function mountDrive(): Promise<MountStatusView> {
    return normalizeMountStatus(await rawMountDrive());
}

export async function listMountableDrives(): Promise<MountableDrive[]> {
    return normalizeMountableDrives(await rawListChannels());
}

export async function mountDrives(channelIds: readonly number[]): Promise<MountStatusView> {
    const selected = [...new Set(channelIds)]
        .filter((id) => Number.isSafeInteger(id) && id > 0);
    if (selected.length === 0) throw new Error('Select at least one drive to mount.');
    return normalizeMountStatus(await rawMountDrives(selected));
}

export async function getMountStatus(): Promise<MountStatusView> {
    return normalizeMountStatus(await rawMountStatus());
}

export async function unmountDrive(): Promise<MountStatusView> {
    return normalizeMountStatus(await rawUnmountDrive());
}

export interface MediaOpenInfo {
    channelId: number;
    fileId: number;
    revision: number;
    name: string;
    storedSize: number;
    plaintextSize: number;
    encrypted: boolean;
    multipart: boolean;
}

export interface MediaOpenResult {
    token: string;
    url: string;
    thumbnailUrl: string;
    name: string;
    kind: string;
    mimeType: string;
    supportsRange: boolean;
    info: MediaOpenInfo;
}

export interface NativeMediaRect {
    x: number;
    y: number;
    width: number;
    height: number;
}

export interface NativeMediaOpenResult {
    token: string;
    thumbnailUrl: string;
    htmlControls: boolean;
    presentation: "embedded" | "standalone";
    initialState: NativeMediaStatePayload | null;
    name: string;
    info: MediaOpenInfo;
}

export interface NativeMediaStatePayload {
    token?: string;
    sequence?: number;
    status?: string;
    error?: unknown;
    eof?: boolean;
    paused?: boolean;
    current_time?: number;
    duration?: number;
    buffered?: Array<{ start?: number; end?: number }>;
    volume?: number;
    muted?: boolean;
    rate?: number;
    loading?: boolean;
    tracks?: unknown;
}

export interface MediaPlaybackUpdate {
    token: string;
    currentTime: number;
    duration: number;
    bufferAhead: number;
}

export interface ThroughputStats {
    bytesPerSecond: number;
    recentFloodWait: boolean;
    lastFloodWaitSeconds: number;
}

export interface MediaStats {
    playback: ThroughputStats;
    thumbnails: ThroughputStats;
}

export function toFileItem(f: backend.FileMetaData): FileItem {
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

export function toFolderItem(d: backend.Folder): FolderItem {
    return {
        id: String(d.id ?? ""),
        name: String(d.name ?? ""),
        parentId: String(d.parent_id ?? ""),
    };
}

export function toRootFile(f: main.TDriveFile): RootFile {
    return {
        msgId: Number(f.id ?? 0),
        name: String(f.name ?? ""),
        size: Number(f.size ?? 0),
        accessHash: Number(f.access_hash ?? 0),
        date: Number(f.date ?? 0),
    };
}

export function toSearchHit(h: backend.SearchResult): SearchHit {
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
    const fs = await rawGetFolderContents(parentId);
    return {
        folders: (fs?.folders ?? []).map(toFolderItem),
        files: (fs?.files ?? []).map(toFileItem),
    };
}

/** Flat list of root files read straight from Telegram history, normalized. */
export async function getFileList(): Promise<RootFile[]> {
    const files = await rawGetFileList();
    return (files ?? []).map(toRootFile);
}

/** Search files and folders in the active drive, normalized. */
export async function search(query: string, limit: number): Promise<SearchHit[]> {
    const hits = await rawSearch(query, limit);
    return (hits ?? []).map(toSearchHit);
}

/** Every image in the active drive, newest first, for the Photos gallery. */
export async function getMedia(): Promise<FileItem[]> {
    const files = await rawListMedia();
    return (files ?? []).map(toFileItem);
}

/**
 * A downscaled JPEG thumbnail for one image, as a ready-to-use data URL.
 * Rejects when the backend can't produce one (unsupported, too large, or a
 * locked encrypted drive) so the caller can render the right placeholder.
 */
export async function getThumbnail(msgId: number): Promise<string> {
    const payload = await rawThumbnail(msgId);
    const dataBase64 = String(payload?.data_base64 ?? "");
    const mimeType = String(payload?.mime_type ?? "");
    if (!dataBase64 || !mimeType) throw new Error("thumbnail unavailable");
    return `data:${mimeType};base64,${dataBase64}`;
}

/** Open a short-lived loopback media URL for a projected file. */
export async function openMedia(msgId: number): Promise<MediaOpenResult> {
    const opened = await rawOpenMedia(msgId);
    return normalizeMediaOpenResult(opened);
}

/** Open a loopback stream URL for a projected audio/PDF/text file. */
export async function openStream(msgId: number): Promise<MediaOpenResult> {
    const opened = await rawOpenStream(msgId);
    return normalizeMediaOpenResult(opened);
}

function normalizeMediaOpenResult(opened?: media.OpenResult): MediaOpenResult {
    return {
        token: String(opened?.token ?? ""),
        url: String(opened?.url ?? ""),
        thumbnailUrl: String(opened?.thumbnail_url ?? ""),
        name: String(opened?.name ?? ""),
        kind: String(opened?.kind ?? ""),
        mimeType: String(opened?.mime_type ?? ""),
        supportsRange: Boolean(opened?.supports_range),
        info: normalizeMediaOpenInfo(opened?.info, opened?.name),
    };
}

function normalizeMediaOpenInfo(info?: media.LogicalFile, fallbackName?: string): MediaOpenInfo {
    return {
        channelId: Number(info?.channel_id ?? 0),
        fileId: Number(info?.file_id ?? 0),
        revision: Number(info?.revision ?? 0),
        name: String(info?.name ?? fallbackName ?? ""),
        storedSize: Number(info?.stored_size ?? 0),
        plaintextSize: Number(info?.plaintext_size ?? 0),
        encrypted: Boolean(info?.encrypted),
        multipart: Boolean(info?.multipart),
    };
}

/** Release the media session and its range-reader cache. */
export async function closeMedia(token: string): Promise<void> {
    if (!token) return;
    await rawCloseMedia(token);
}

/** Open a native all-format player for one projected file. */
export async function openNativeMedia(msgId: number, rect: NativeMediaRect): Promise<NativeMediaOpenResult> {
    const opened = await rawOpenNativeMedia(msgId, rect);
    return normalizeNativeMediaOpenResult(opened);
}

/** Promote an existing webview stream to native playback without reopening it. */
export async function attachNativeMedia(token: string, rect: NativeMediaRect): Promise<NativeMediaOpenResult> {
    if (!token) throw new Error("Media session is required.");
    const opened = await rawAttachNativeMedia(token, rect);
    return normalizeNativeMediaOpenResult(opened);
}

function normalizeNativeMediaOpenResult(opened?: main.NativeMediaResult): NativeMediaOpenResult {
    const token = String(opened?.token ?? "");
    const rawInitialState = opened?.initial_state;
    const initialStateRecord = rawInitialState !== null
        && typeof rawInitialState === "object"
        && !Array.isArray(rawInitialState)
        ? rawInitialState as NativeMediaStatePayload
        : null;
    const initialState: NativeMediaStatePayload | null = initialStateRecord
        ? { ...initialStateRecord, token: String(initialStateRecord.token || token) }
        : null;
    return {
        token,
        thumbnailUrl: String(opened?.thumbnail_url ?? ""),
        htmlControls: Boolean(opened?.html_controls),
        presentation: opened?.presentation === "standalone" ? "standalone" : "embedded",
        initialState,
        name: String(opened?.name ?? ""),
        info: normalizeMediaOpenInfo(opened?.info, opened?.name),
    };
}

export async function resizeNativeMedia(token: string, rect: NativeMediaRect): Promise<void> {
    if (!token) return;
    await rawResizeNativeMedia(token, rect);
}

export async function nativeMediaCommand(token: string, command: string[]): Promise<void> {
    if (!token || command.length === 0) return;
    await rawNativeMediaCommand(token, command);
}

export async function closeNativeMedia(token: string): Promise<void> {
    if (!token) return;
    await rawCloseNativeMedia(token);
}

// Windows paints seek previews above its child mpv window because the webview
// cannot layer HTML over that native surface. Other players deliberately no-op.
// imageBase64 is a JPEG/PNG frame; rect is the preview box in CSS pixels.
export async function showNativeSeekThumbnail(token: string, imageBase64: string, rect: NativeMediaRect): Promise<void> {
    if (!token || !imageBase64) return;
    await rawShowNativeSeekThumbnail(token, imageBase64, rect);
}

export async function moveNativeSeekThumbnail(token: string, rect: NativeMediaRect): Promise<void> {
    if (!token) return;
    await rawMoveNativeSeekThumbnail(token, rect);
}

export async function hideNativeSeekThumbnail(token: string): Promise<void> {
    if (!token) return;
    await rawHideNativeSeekThumbnail(token);
}

export async function updateMediaPlayback(update: MediaPlaybackUpdate): Promise<void> {
    if (!update.token) return;
    await rawUpdateMediaPlayback({
        token: update.token,
        current_time: update.currentTime,
        duration: update.duration,
        buffer_ahead: update.bufferAhead,
    });
}

function toThroughputStats(stats?: media.ThroughputStats): ThroughputStats {
    return {
        bytesPerSecond: Number(stats?.bytes_per_second ?? 0),
        recentFloodWait: Boolean(stats?.recent_flood_wait),
        lastFloodWaitSeconds: Number(stats?.last_flood_wait_seconds ?? 0),
    };
}

export async function getMediaStats(token: string): Promise<MediaStats> {
    if (!token) {
        return {
            playback: toThroughputStats(),
            thumbnails: toThroughputStats(),
        };
    }
    const stats = await rawGetMediaStats(token);
    return {
        playback: toThroughputStats(stats?.playback),
        thumbnails: toThroughputStats(stats?.thumbnails),
    };
}
