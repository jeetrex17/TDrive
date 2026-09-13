import {
    AppVersion as rawAppVersion,
    CancelUpdateDownload as rawCancelUpdateDownload,
    CheckForUpdate as rawCheckForUpdate,
    DownloadUpdate as rawDownloadUpdate,
    GetUpdateState as rawGetUpdateState,
    InstallUpdateAndRestart as rawInstallUpdateAndRestart,
    OpenUpdatePage as rawOpenUpdatePage,
} from "../../wailsjs/go/main/App";
import type { AppVersion, UpdateSnapshot } from "../types";
import { asRecord, nonNegativeNumber } from "./shared";
import { onRuntimeEvent } from "./runtime";

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
export function onUpdateState(callback: (snapshot: UpdateSnapshot) => void): (() => void) | null {
    return onRuntimeEvent<[unknown]>("update_state", (payload) => callback(normalizeUpdateSnapshot(payload)));
}