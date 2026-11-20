import { get, writable } from 'svelte/store';
import {
    cancelSourceCopies,
    createSourceCopyFolder,
    getMyUserId,
    listSourceCopies,
    listSourceCopyFolders,
    previewSourceCopyNames,
    retrySourceCopy,
    saveSourceCopies,
    type SourceCopyFolder,
    type SourceCopyInput,
    type SourceCopyResult,
} from '../api';
import { invalidateFolderIndex, state } from '../state';
import { canSaveSourceMedia, MAX_SOURCE_COPY_SELECTION, type ChannelMediaItem, type ChannelSource } from '../ui/channels/channel-model';
import { closeChannel } from '../ui/channels/channel-store';
import { appActions } from './app-actions';
import { switchActiveChannel } from './channels';
import { humanizeBackendError } from './errors';
import { renderBreadcrumb } from './navigation';
import { notify, notifyAppError } from './notifications';

interface CopyItem extends SourceCopyInput {
    originalName: string;
}

export interface SharedCopyFile {
    id: number;
    name: string;
    revision: number;
    identity: string;
}

interface CopyDialog {
    accountId: number;
    fromSharedDrive: boolean;
    suggestedFolder: string;
    items: readonly CopyItem[];
    path: SourceCopyFolder[];
    folders: SourceCopyFolder[];
    driveId: number;
    stage: 'loading' | 'ready' | 'checking' | 'creating' | 'saving' | 'done';
    browseFailed: boolean;
    stopping: boolean;
    error: string;
    results: readonly SourceCopyResult[];
}

export const sourceCopyDialog = writable<CopyDialog | null>(null);
export const sourceCopyActivity = writable<readonly SourceCopyResult[]>([]);
export const activeCopyChecks = writable<ReadonlySet<string>>(new Set());

let session = 0;
let active = false;
let accountId = 0;
let dialogVersion = 0;
let activityVersion = 0;

function updateDialog(update: Partial<CopyDialog>): void {
    sourceCopyDialog.update((current) => current ? { ...current, ...update } : null);
}

function remember(results: readonly SourceCopyResult[]): void {
    const recorded = results.filter(({ id }) => id);
    const ids = new Set(recorded.map(({ id }) => id));
    activityVersion += 1;
    sourceCopyActivity.update((previous) => [
        ...recorded.filter(({ status }) => status === 'pending'),
        ...previous.filter(({ id }) => !ids.has(id)),
    ]);
}

async function loadActivity(identity: number, activation: number, includeFinished = false): Promise<void> {
    const version = activityVersion;
    const results = await listSourceCopies(identity);
    if (activation === session && version === activityVersion && identity === accountId) {
        sourceCopyActivity.set(includeFinished ? results : results.filter(({ status }) => status === 'pending'));
    }
}

export function activateSourceCopies(): () => void {
    const activation = ++session;
    active = true;
    accountId = 0;
    void getMyUserId().then((identity) => {
        if (activation !== session || identity <= 0) return;
        accountId = identity;
        return loadActivity(identity, activation);
    }).catch(() => {
        // Opening a save remains available from a source with an account ID.
    });
    return () => {
        session += 1;
        active = false;
        accountId = 0;
        dialogVersion += 1;
        sourceCopyDialog.set(null);
        sourceCopyActivity.set([]);
        activeCopyChecks.set(new Set());
    };
}

function stillOpen(version: number): boolean {
    return active && version === dialogVersion;
}

function destination(path: readonly SourceCopyFolder[]): string {
    return path[path.length - 1]?.id ?? '';
}

async function browse(path: SourceCopyFolder[], initial = false): Promise<void> {
    const current = get(sourceCopyDialog);
    if (!current) return;
    const version = ++dialogVersion;
    updateDialog({ path, folders: [], stage: 'loading', browseFailed: false, error: '' });
    try {
        const [page, names] = await Promise.all([
            listSourceCopyFolders(current.accountId, destination(path)),
            initial ? previewSourceCopyNames(current.items.map(({ name }) => name)) : null,
        ]);
        if (!stillOpen(version)) return;
        if (current.driveId && current.driveId !== page.driveId) {
            throw new Error('My Drive changed. Close this dialog and select the files again.');
        }
        const folders = [...page.folders].sort((left, right) => left.name.localeCompare(right.name));
        const items = names ? current.items.map((item, index) => ({ ...item, name: names[index] ?? item.name })) : current.items;
        updateDialog({ folders, driveId: page.driveId, items, stage: 'ready' });
    } catch (error) {
        if (stillOpen(version)) updateDialog({ stage: 'ready', browseFailed: true, error: humanizeBackendError(error) });
    }
}

export function openSourceCopy(source: ChannelSource, posts: readonly ChannelMediaItem[], suggestedFolder = source.title): void {
    const unique = new Map(posts.filter((post) => canSaveSourceMedia(source, post)).map((post) => [post.msgId, post]));
    const identity = Number(source.accountId);
    const items = [...unique.values()].map((post): CopyItem => ({
        name: post.name || 'Telegram file',
        originalName: post.name,
        source: {
            account_id: identity,
            peer_kind: source.peerKind,
            peer_id: source.peerId,
            generation: source.generation,
            msg_id: post.msgId,
            identity: post.copyIdentity ?? '',
        },
    }));
    openCopyDialog(identity, false, suggestedFolder, items);
}

export function openSharedDriveCopy(identity: number, drive: { id: number; title: string }, files: readonly SharedCopyFile[]): void {
    const items = files.map((file): CopyItem => ({
        name: file.name,
        originalName: file.name,
        source: {
            account_id: identity,
            peer_kind: 'shared-drive',
            peer_id: drive.id,
            generation: String(file.revision),
            msg_id: file.id,
            identity: file.identity,
        },
    }));
    openCopyDialog(identity, true, drive.title, items);
}

function openCopyDialog(identity: number, fromSharedDrive: boolean, suggestedFolder: string, items: readonly CopyItem[]): void {
    if (!active || get(sourceCopyDialog) || !Number.isSafeInteger(identity) || identity <= 0 ||
        (accountId && accountId !== identity) || items.length === 0 || items.length > MAX_SOURCE_COPY_SELECTION) return;
    if (!accountId) accountId = identity;
    sourceCopyDialog.set({
        accountId: identity, fromSharedDrive, suggestedFolder, items,
        path: [], folders: [], driveId: 0, stage: 'loading',
        browseFailed: false, stopping: false, error: '', results: [],
    });
    void browse([], true);
}

export function closeSourceCopy(): void {
    const stage = get(sourceCopyDialog)?.stage;
    if (stage === 'saving' || stage === 'creating') return;
    dialogVersion += 1;
    sourceCopyDialog.set(null);
}

export function openSourceCopyFolder(folder: SourceCopyFolder): void {
    const current = get(sourceCopyDialog);
    if (current?.stage === 'ready') void browse([...current.path, folder]);
}

export function browseSourceCopyCrumb(index: number): void {
    const current = get(sourceCopyDialog);
    if (current?.stage === 'ready') void browse(current.path.slice(0, index + 1));
}

export function reloadSourceCopyFolder(): void {
    const current = get(sourceCopyDialog);
    if (current?.stage === 'ready') void browse(current.path, !current.driveId);
}

export function editSourceCopyName(name: string): void {
    const current = get(sourceCopyDialog);
    if (current?.stage !== 'ready' || current.items.length !== 1) return;
    updateDialog({ items: [{ ...current.items[0], name }], error: '' });
}

export async function addSourceCopyFolder(name: string): Promise<string | null> {
    const current = get(sourceCopyDialog);
    if (current?.stage !== 'ready' || current.browseFailed || !name.trim()) return null;
    const version = dialogVersion;
    updateDialog({ stage: 'creating', error: '' });
    try {
        const [canonical] = await previewSourceCopyNames([name]);
        if (!stillOpen(version)) return null;
        if (canonical && canonical !== name) {
            updateDialog({ stage: 'ready', error: 'Folder name adjusted. Review it, then select Create folder.' });
            return canonical;
        }
        const folder = await createSourceCopyFolder(current.accountId, current.driveId, destination(current.path), name);
        if (stillOpen(version)) void browse([...current.path, folder]);
    } catch (error) {
        if (stillOpen(version)) updateDialog({ stage: 'ready', error: humanizeBackendError(error) });
    }
    return null;
}

export async function confirmSourceCopy(): Promise<void> {
    const current = get(sourceCopyDialog);
    if (current?.stage !== 'ready' || current.browseFailed || !current.driveId || current.items.some(({ name }) => !name.trim())) return;
    const version = dialogVersion;
    const activation = session;
    updateDialog({ stage: 'checking', error: '' });
    try {
        const names = await previewSourceCopyNames(current.items.map(({ name }) => name));
        if (!stillOpen(version)) return;
        if (current.items.some(({ name }, index) => name !== names[index])) {
            updateDialog({
                items: current.items.map((item, index) => ({ ...item, name: names[index] ?? item.name })),
                stage: 'ready',
                error: current.fromSharedDrive ? 'File names adjusted. Review them, then copy.' : 'File names adjusted. Review them, then save.',
            });
            return;
        }
        updateDialog({ stage: 'saving' });
        const results = await saveSourceCopies(current.accountId, current.driveId, destination(current.path), current.items, true);
        if (activation !== session) return;
        remember(results);
        if (results.some(({ status }) => status === 'saved')) invalidateFolderIndex(current.driveId);
        if (stillOpen(version)) updateDialog({ stage: 'done', stopping: false, results });
    } catch (error) {
        if (!stillOpen(version)) return;
        const wasSaving = get(sourceCopyDialog)?.stage === 'saving';
        // A lost call response can hide a committed save. Surface its journal
        // in Activity and keep the dialog from starting a second publication.
        updateDialog({ stage: wasSaving ? 'done' : 'ready', stopping: false, error: humanizeBackendError(error) });
        if (wasSaving) void loadActivity(current.accountId, activation, true).catch(() => {});
    }
}

export async function stopSourceCopy(): Promise<void> {
    const current = get(sourceCopyDialog);
    if (current?.stage !== 'saving' || current.stopping) return;
    const version = dialogVersion;
    updateDialog({ stopping: true, error: '' });
    try {
        const stopping = await cancelSourceCopies(current.accountId);
        if (!stopping && stillOpen(version) && get(sourceCopyDialog)?.stage === 'saving') {
            updateDialog({ stopping: false });
        }
    } catch (error) {
        if (stillOpen(version) && get(sourceCopyDialog)?.stage === 'saving') updateDialog({ stopping: false, error: humanizeBackendError(error) });
    }
}

export async function openSavedSourceCopyFolder(): Promise<void> {
    const current = get(sourceCopyDialog);
    if (current?.stage !== 'done' || !current.results.some(({ status }) => status === 'saved')) return;
    const activation = session;
    closeSourceCopy();
    closeChannel();
    if (state.activeChannel?.id !== current.driveId) await switchActiveChannel(current.driveId);
    if (activation !== session || state.activeChannel?.id !== current.driveId || state.virtualView) return;
    state.folderPath = current.path;
    state.currentFolderId = destination(current.path);
    renderBreadcrumb();
    appActions().refreshFiles();
}

export async function checkSourceCopy(id: string): Promise<void> {
    const current = get(sourceCopyActivity).find((result) => result.id === id);
    if (!active || !accountId || current?.status !== 'pending' || get(activeCopyChecks).has(id)) return;
    const activation = session;
    activeCopyChecks.update((previous) => new Set([...previous, id]));
    try {
        const result = await retrySourceCopy(accountId, id);
        if (activation !== session) return;
        remember([result]);
        if (result.status === 'saved') {
            const drive = state.channels.find(({ kind }) => kind === 'personal');
            if (drive) invalidateFolderIndex(drive.id);
            notify({ level: 'success', title: 'Saved to My Drive', body: result.name });
        } else if (result.status === 'failed' || result.status === 'canceled') {
            notify({ level: 'warning', title: 'Copy did not finish', body: result.message || result.name });
        }
    } catch (error) {
        if (activation === session) notifyAppError(error, { title: 'Could not check the copy' });
    } finally {
        if (activation === session) activeCopyChecks.update((previous) => new Set([...previous].filter((entry) => entry !== id)));
    }
}

export function clearSourceCopyHistory(): void {
    activityVersion += 1;
    sourceCopyActivity.update((results) => results.filter(({ status }) => status === 'pending'));
}
