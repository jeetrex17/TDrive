/**
 * Asking Android for a folder.
 *
 * Wails refuses directory selection on Android, and its reason is sound as far
 * as it goes: the Storage Access Framework returns document-tree URIs, and its
 * dialog API has nowhere to put one. So this does not go through that API. The
 * app's own Android bridge walks the chosen tree and answers with a manifest,
 * then copies files into the app cache on demand, a handful at a time.
 *
 * The manifest is the whole point. Copying the tree up front is simpler, but a
 * 5 GB folder then needs 5 GB of free cache and a long silent wait before a
 * single byte is uploaded. With the manifest the cache only ever holds the
 * window currently going up.
 */

import { callBridge, hasBridgeMethod } from './android-bridge';

/** One file in the picked tree. Nothing is copied until it is materialized. */
export interface AndroidFolderFile {
    /** Opaque to us; the bridge resolves it back to a document URI. */
    id: string;
    /** Path below the picked folder, "/" separated. */
    rel: string;
    size: number;
}

export interface AndroidFolderManifest {
    root: string;
    files: AndroidFolderFile[];
}

/** Whether this build can ask for a folder at all. */
export function canPickFolder(): boolean {
    return hasBridgeMethod('pickFolder');
}

function parseManifest(raw: string): AndroidFolderManifest | null {
    if (!raw) return null;
    const parsed = JSON.parse(raw) as { root?: unknown; files?: unknown };
    const entries = Array.isArray(parsed.files) ? parsed.files : [];
    const files: AndroidFolderFile[] = [];
    for (const entry of entries) {
        const file = entry as { id?: unknown; rel?: unknown; size?: unknown };
        const id = String(file.id ?? '');
        const rel = String(file.rel ?? '');
        if (id && rel) files.push({ id, rel, size: Math.max(0, Number(file.size) || 0) });
    }
    return { root: String(parsed.root ?? ''), files };
}

/**
 * Opens the system folder picker and resolves with what is in the chosen tree,
 * or with null if it was dismissed. The walk happens on the bridge side and is
 * slow on a deep tree, so this can take seconds after the dialog closes.
 */
export function pickAndroidFolder(): Promise<AndroidFolderManifest | null> {
    return callBridge('pickFolder', [], 'This build cannot open a folder picker.').then(parseManifest);
}

/**
 * Copies the named files into the app cache and resolves with their local
 * paths. An id the bridge could not read is absent from the result rather than
 * failing the whole batch.
 */
export async function materializeAndroidFiles(ids: string[]): Promise<Map<string, string>> {
    const raw = await callBridge('materializeFiles', [JSON.stringify(ids)], 'This build cannot read folder contents.');
    const parsed = JSON.parse(raw || '{}') as { paths?: unknown };
    const record = parsed.paths && typeof parsed.paths === 'object' ? parsed.paths as Record<string, unknown> : {};
    const paths = new Map<string, string>();
    for (const [id, path] of Object.entries(record)) {
        const local = String(path ?? '');
        if (local) paths.set(id, local);
    }
    return paths;
}

/** Drops the cache copies made for these ids. Safe for ids never materialized. */
export async function releaseAndroidFiles(ids: string[]): Promise<void> {
    await callBridge('releaseFiles', [JSON.stringify(ids)], 'This build cannot release folder contents.');
}

function pathSegments(path: string): string[] {
    return path.split('/').filter((segment) => segment && segment !== '.' && segment !== '..');
}

/** The directory a manifest entry belongs in, "" for the top of the tree. */
export function folderPathFor(root: string, rel: string): string {
    const segments = pathSegments(`${root}/${rel}`);
    segments.pop();
    return segments.join('/');
}

/**
 * Every directory the manifest implies, shallowest first, because a folder
 * cannot be created before the parent whose id it needs.
 */
export function folderPathsFor(manifest: AndroidFolderManifest): string[] {
    const paths = new Set<string>();
    const root = pathSegments(manifest.root).join('/');
    if (root) paths.add(root);
    for (const file of manifest.files) {
        const segments = pathSegments(folderPathFor(manifest.root, file.rel));
        for (let depth = 1; depth <= segments.length; depth += 1) {
            paths.add(segments.slice(0, depth).join('/'));
        }
    }
    return [...paths].sort((a, b) => a.split('/').length - b.split('/').length);
}
