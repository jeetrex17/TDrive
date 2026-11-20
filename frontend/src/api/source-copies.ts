import {
    CancelSourceCopies as cancel,
    CreateSourceCopyFolder as createFolder,
    ListSourceCopies as listCopies,
    ListSourceCopyFolders as listFolders,
    PreviewSourceCopyNames as previewNames,
    RetrySourceCopy as retry,
    SaveSourceCopies as save,
} from '../../bindings/TDrive/internal/app/driveservice';
import type { SourceCopyInput as BoundSourceCopyInput } from '../../bindings/TDrive/backend/services/file/models';
import { invokeBackend } from './gateway';

export type SourceCopyInput = BoundSourceCopyInput;

export interface SourceCopyFolder {
    id: string;
    name: string;
}

export interface SourceCopyResult {
    id: string;
    name: string;
    parentId: string;
    status: 'saved' | 'pending' | 'failed' | 'canceled';
    fileId: number;
    message: string;
}

function copyResult(value: { id: string; name: string; parent_id: string; status: string; file_id: number; message: string }): SourceCopyResult {
    const status = value.status === 'saved' || value.status === 'canceled' || value.status === 'failed'
        ? value.status
        : 'pending';
    return { id: value.id, name: value.name, parentId: value.parent_id, status, fileId: value.file_id, message: value.message };
}

export async function listSourceCopyFolders(accountId: number, parentId: string): Promise<{ driveId: number; folders: SourceCopyFolder[] }> {
    const result = await invokeBackend(listFolders, accountId, parentId);
    const folders = (result.folders ?? []).map(({ id, name }) => ({ id, name }));
    return { driveId: result.drive_id, folders };
}

export async function createSourceCopyFolder(accountId: number, driveId: number, parentId: string, name: string): Promise<SourceCopyFolder> {
    const folder = await invokeBackend(createFolder, accountId, driveId, parentId, name);
    return { id: folder.id, name: folder.name };
}

export async function previewSourceCopyNames(names: string[]): Promise<string[]> {
    return (await invokeBackend(previewNames, names)) ?? [];
}

export async function saveSourceCopies(
    accountId: number,
    driveId: number,
    parentId: string,
    items: readonly SourceCopyInput[],
    allowPlain: boolean,
): Promise<SourceCopyResult[]> {
    const input = items.map(({ source, name }) => ({ source, name }));
    return ((await invokeBackend(save, accountId, driveId, parentId, input, allowPlain)) ?? []).map(copyResult);
}

export async function cancelSourceCopies(accountId: number): Promise<boolean> {
    return invokeBackend(cancel, accountId);
}

export async function listSourceCopies(accountId: number): Promise<SourceCopyResult[]> {
    return ((await invokeBackend(listCopies, accountId)) ?? []).map(copyResult);
}

export async function retrySourceCopy(accountId: number, id: string): Promise<SourceCopyResult> {
    return copyResult(await invokeBackend(retry, accountId, id));
}
