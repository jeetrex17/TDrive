/**
 * Moving a finished download out of the sandbox and into public Downloads.
 *
 * Go writes every phone download into the app's private storage, because that
 * is the only place it can write. On iOS that is the end of it: the folder is
 * declared shareable and the Files app lists it. Android gives nothing inside
 * the sandbox to the Files app -- since Android 11 it cannot even browse
 * Android/data -- so a download left there is invisible and, for a folder,
 * completely unreachable. The host moves it to the public Downloads collection
 * instead, which needs no permission and is the folder every file manager and
 * the Downloads app already open on.
 */

import { callBridge, hasBridgeMethod } from './android-bridge';

/** Whether this build can move a download into public storage. */
export function canSaveToDownloads(): boolean {
    return hasBridgeMethod('saveToDownloads');
}

/**
 * Moves the file or folder at a sandbox path into public Downloads and
 * resolves with where it landed, ready to be shown to the user ("Download/
 * plan.pdf"). Resolves with "" when the host could not say.
 *
 * The move is the host's to make: it owns the MediaStore entry, it renames
 * around a name already taken, and it deletes the sandbox copy only once every
 * byte is safely across, so a failure here never costs the download.
 */
export async function saveToDownloads(path: string): Promise<string> {
    if (!path) return '';
    const raw = await callBridge(
        'saveToDownloads',
        [JSON.stringify({ path })],
        'This build cannot save to Downloads.',
    );
    if (!raw) return '';
    const parsed = JSON.parse(raw) as { location?: unknown };
    return String(parsed.location ?? '');
}
