// Public boundary for the trash: what deletion moved aside, and the two ways
// back out of it. The namespace access mirrors api/photo-backup: the generated
// Wails bindings arrive with the backend change, while this module stays the
// single typed frontend boundary and fails loudly on a build that lacks them.
import * as appBindings from '../../bindings/TDrive/app';
import type { OperationResult } from '../types';
import { invokeBackend } from './gateway';
import { normalizeOperationResult } from './operation';
import { asRecord, boundedText, nonNegativeNumber } from './shared';

type AppBinding = (...args: unknown[]) => unknown;
const bindings = appBindings as unknown as Record<string, AppBinding>;

function binding(name: string): AppBinding {
    const value = bindings[name];
    if (typeof value !== 'function') throw new Error(`${name} is unavailable in this build.`);
    return value;
}

/** A folder carries no bytes of its own; its files are listed separately. */
export type TrashKind = 'file' | 'folder';

export interface TrashEntry {
    /** Opaque backend handle ("f:2615", "d:<uuid>"); the identity for every mutation. */
    objectId: string;
    kind: TrashKind;
    name: string;
    /** Human path the item was deleted from; empty means the drive root. */
    parentPath: string;
    size: number;
    /** Unix milliseconds. */
    deletedAt: number;
    /** Unix milliseconds after which the backend purges the item for good. */
    purgeAfter: number;
}

// Bounds are deliberately generous for real names and paths and hard enough
// that a malformed row cannot push an unbounded string into the DOM.
const MAX_OBJECT_ID = 128;
const MAX_NAME = 512;
const MAX_PARENT_PATH = 1024;

/**
 * One row, or null when it carries no usable identity. A row without an id
 * cannot be restored or purged, so showing it would only offer dead controls.
 */
export function normalizeTrashEntry(value: unknown): TrashEntry | null {
    const raw = asRecord(value);
    const objectId = boundedText(raw.object_id, MAX_OBJECT_ID);
    if (!objectId) return null;
    const kind: TrashKind = raw.kind === 'folder' ? 'folder' : 'file';
    return {
        objectId,
        kind,
        name: boundedText(raw.name, MAX_NAME) || 'Untitled',
        parentPath: boundedText(raw.parent_path, MAX_PARENT_PATH),
        // A folder's reported size is not a fact about its own bytes.
        size: kind === 'folder' ? 0 : nonNegativeNumber(raw.size),
        deletedAt: nonNegativeNumber(raw.deleted_at),
        purgeAfter: nonNegativeNumber(raw.purge_after),
    };
}

export function normalizeTrashEntries(value: unknown): TrashEntry[] {
    if (!Array.isArray(value)) return [];
    const entries: TrashEntry[] = [];
    for (const item of value) {
        const entry = normalizeTrashEntry(item);
        if (entry) entries.push(entry);
    }
    return entries;
}

/** ListTrash answers with the rows directly, not the operation envelope. */
export async function listTrash(): Promise<TrashEntry[]> {
    return normalizeTrashEntries(await invokeBackend(binding('ListTrash')));
}

// The mutations answer with the operation envelope, so a refusal reaches the
// user in the backend's own words instead of a wrapped call error.
export async function restoreFromTrash(objectId: string): Promise<OperationResult> {
    return normalizeOperationResult(await invokeBackend(binding('RestoreFromTrash'), objectId), 'Could not restore this item');
}

export async function deleteFromTrashPermanently(objectId: string): Promise<OperationResult> {
    return normalizeOperationResult(await invokeBackend(binding('DeleteFromTrashPermanently'), objectId), 'Could not delete this item');
}

export async function emptyTrash(): Promise<OperationResult> {
    return normalizeOperationResult(await invokeBackend(binding('EmptyTrash')), 'Could not empty the trash');
}
