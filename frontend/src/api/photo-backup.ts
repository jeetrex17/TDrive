// Public boundary for the durable photo-backup queue. The namespace access is
// intentional: generated Wails bindings arrive with the backend change, while
// this module remains the single typed frontend boundary.
import * as appBindings from '../../bindings/TDrive/app';
import { invokeBackend } from './gateway';
import { asRecord, boundedText, nonNegativeNumber } from './shared';

type AppBinding = (...args: unknown[]) => unknown;
const bindings = appBindings as unknown as Record<string, AppBinding>;

function binding(name: string): AppBinding {
    const value = bindings[name];
    if (typeof value !== 'function') throw new Error(`${name} is unavailable in this build.`);
    return value;
}

export type BackupMediaType = 'photo' | 'video';
export interface PhotoBackupSettings { enabled: boolean; photos: boolean; videos: boolean; futureOnly: boolean; wifiOnly: boolean; encrypt: boolean; }
export interface PhotoBackupSource { id: string; kind: string; name: string; root: string; enabled: boolean; addedAt: number; }
export interface PhotoBackupStatus { phase: 'idle' | 'scanning' | 'queued' | 'uploading' | 'paused' | 'complete' | 'failed'; pending: number; uploading: number; complete: number; failed: number; paused: number; bytesDone: number; bytesTotal: number; currentFile: string; currentFileBytesDone: number; currentFileBytesTotal: number; currentFilePercent: number; message: string; }
export interface BackupPolicyCapability { supported: boolean; label: string; detail: string; }
export interface PhotoBackupCapabilities { wifiOnly: BackupPolicyCapability; access: { status: string; detail: string }; }
export interface PhotoBackupState { settings: PhotoBackupSettings; sources: PhotoBackupSource[]; status: PhotoBackupStatus; capabilities: PhotoBackupCapabilities; platform: string; destination: { id: string; title: string; kind: string }; manualPaused: boolean; encryptionRequired: boolean; }
export interface PhotoBackupAsset { id: string; version: string; name: string; mediaType: BackupMediaType; modifiedAt: number; size: number; resourceId?: string; }

const defaultSettings: PhotoBackupSettings = { enabled: false, photos: true, videos: true, futureOnly: false, wifiOnly: false, encrypt: false };

export const futureOnlyDescription = 'Only photos and videos added after you turn this on will be backed up. Existing items are not included.';

function normalizeAsset(value: unknown): PhotoBackupAsset | null {
    const raw = asRecord(value); const id = boundedText(raw.id, 512); const version = boundedText(raw.version, 256);
    const mediaType = raw.media_type === 'video' ? 'video' : raw.media_type === 'photo' ? 'photo' : null;
    if (!id || !version || !mediaType) return null;
    return { id, version, name: boundedText(raw.name, 512) || 'Untitled', mediaType, modifiedAt: nonNegativeNumber(raw.modified_at), size: nonNegativeNumber(raw.size), resourceId: boundedText(raw.resource_id, 512) || undefined };
}

export function normalizePhotoBackupState(value: unknown): PhotoBackupState {
    const raw = asRecord(value); const settings = asRecord(raw.settings); const status = asRecord(raw.status); const capabilities = asRecord(raw.capabilities);
    const policy = (key: string): BackupPolicyCapability => { const entry = asRecord(capabilities[key]); const supported = Boolean(entry.supported); return { supported, label: boundedText(entry.label, 120) || (supported ? '' : 'Unavailable on this device'), detail: boundedText(entry.detail, 240) }; };
    const sources = Array.isArray(raw.sources) ? raw.sources.map((item): PhotoBackupSource | null => { const source = asRecord(item); const id = boundedText(source.id, 512); return id ? { id, kind: boundedText(source.kind, 64), name: boundedText(source.name, 240) || 'Photo library', root: boundedText(source.root, 1024), enabled: source.enabled !== false, addedAt: nonNegativeNumber(source.added_at) } : null; }).filter((item): item is PhotoBackupSource => item !== null) : [];
    const phases: PhotoBackupStatus['phase'][] = ['idle', 'scanning', 'queued', 'uploading', 'paused', 'complete', 'failed']; const phase = phases.includes(status.phase as PhotoBackupStatus['phase']) ? status.phase as PhotoBackupStatus['phase'] : 'idle';
    const destination = asRecord(raw.destination);
    return { settings: { enabled: Boolean(settings.enabled), photos: settings.photos !== false, videos: settings.videos !== false, futureOnly: Boolean(settings.future_only), wifiOnly: Boolean(settings.wifi_only), encrypt: Boolean(settings.encrypt) }, sources, status: { phase, pending: nonNegativeNumber(status.pending), uploading: nonNegativeNumber(status.uploading), complete: nonNegativeNumber(status.complete), failed: nonNegativeNumber(status.failed), paused: nonNegativeNumber(status.paused), bytesDone: nonNegativeNumber(status.bytes_done), bytesTotal: nonNegativeNumber(status.bytes_total), currentFile: boundedText(status.current_file, 512), currentFileBytesDone: nonNegativeNumber(status.current_file_bytes_done), currentFileBytesTotal: nonNegativeNumber(status.current_file_bytes_total), currentFilePercent: Math.min(100, nonNegativeNumber(status.current_file_percent)), message: boundedText(status.message, 240) }, capabilities: { wifiOnly: policy('wifi_only'), access: { status: boundedText(asRecord(capabilities.access).status, 64), detail: boundedText(asRecord(capabilities.access).detail, 240) } }, platform: boundedText(raw.platform, 32), destination: { id: boundedText(destination.id, 64), title: boundedText(destination.title, 240), kind: boundedText(destination.kind, 32) }, manualPaused: Boolean(raw.manual_paused), encryptionRequired: Boolean(raw.encryption_required) };
}

export async function getPhotoBackupState(): Promise<PhotoBackupState> { return normalizePhotoBackupState(await invokeBackend(binding('GetPhotoBackupState'))); }
export async function savePhotoBackupSettings(settings: PhotoBackupSettings): Promise<PhotoBackupState> { return normalizePhotoBackupState(await invokeBackend(binding('SavePhotoBackupSettings'), { enabled: settings.enabled, photos: settings.photos, videos: settings.videos, future_only: settings.futureOnly, wifi_only: settings.wifiOnly, encrypt: settings.encrypt })); }
export async function addPhotoBackupFolder(): Promise<PhotoBackupSource | null> { const raw = asRecord(await invokeBackend(binding('AddPhotoBackupFolder'))); const id = boundedText(raw.id, 512); return id ? { id, kind: boundedText(raw.kind, 64), name: boundedText(raw.name, 240) || 'Folder', root: boundedText(raw.root, 1024), enabled: raw.enabled !== false, addedAt: nonNegativeNumber(raw.added_at) } : null; }
export async function upsertPhotoBackupSource(source: PhotoBackupSource): Promise<void> { await invokeBackend(binding('UpsertPhotoBackupSource'), { id: source.id, kind: source.kind, name: source.name, root: source.root, enabled: source.enabled, added_at: source.addedAt }); }
export async function removePhotoBackupSource(id: string): Promise<void> { await invokeBackend(binding('RemovePhotoBackupSource'), id); }
export async function enqueuePhotoBackupAssets(sourceID: string, assets: PhotoBackupAsset[]): Promise<void> { await invokeBackend(binding('EnqueuePhotoBackupAssets'), sourceID, assets.map((asset) => ({ id: asset.id, version: asset.version, name: asset.name, media_type: asset.mediaType, modified_at: asset.modifiedAt, size: asset.size, resource_id: asset.resourceId ?? '' }))); }
export async function runPhotoBackup(): Promise<void> { await invokeBackend(binding('RunPhotoBackup')); }
export async function pausePhotoBackup(): Promise<void> { await invokeBackend(binding('PausePhotoBackup')); }
export async function resumePhotoBackup(): Promise<void> { await invokeBackend(binding('ResumePhotoBackup')); }
export async function retryPhotoBackup(): Promise<void> { await invokeBackend(binding('RetryPhotoBackup')); }
export async function setPhotoBackupPolicy(wifi: boolean): Promise<void> { await invokeBackend(binding('SetPhotoBackupPolicy'), { wifi, observed_at: Date.now() }); }
export async function setPhotoBackupBackgroundLease(active: boolean): Promise<void> { await invokeBackend(binding('SetPhotoBackupBackgroundLease'), active); }
export async function resolvePhotoBackupResource(token: string, path: string, error: string): Promise<void> { await invokeBackend(binding('ResolvePhotoBackupResource'), token, path, error); }
export { defaultSettings, normalizeAsset };
