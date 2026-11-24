// Typed and pasted folder paths for the breadcrumb's path field. Parsing and
// matching live here, apart from the app, so every rule below is testable on
// its own; path-navigation.ts reads the drive and applies the result.

export interface DrivePathFolder {
    id: string;
    name: string;
}

export interface DrivePathFile {
    msgId: number;
    name: string;
}

export interface DriveListing {
    folders: DrivePathFolder[];
    files: DrivePathFile[];
}

export interface ParsedDrivePath {
    segments: string[];
    // A trailing separator names a folder, the way Finder's Go to Folder reads it.
    folderOnly: boolean;
    // Shaped like a path on this computer without a drive letter (/Users/me/..., ~/...).
    // Only worded into a miss at the root; a drive folder of that name still opens.
    localHint?: true;
}

export type DrivePathTarget =
    | { kind: 'folder'; path: DrivePathFolder[] }
    | { kind: 'file'; path: DrivePathFolder[]; fileId: string };

export type DrivePathFailure =
    | { kind: 'empty' }
    | { kind: 'local' }
    | { kind: 'link' }
    | { kind: 'too-long' }
    | { kind: 'not-found' | 'not-a-folder' | 'ambiguous'; name: string; parent: string[]; maybeLocal?: true };

export interface DrivePathSource {
    /** Subfolders and files of one folder; '' is the drive root. */
    list: (parentId: string) => Promise<DriveListing>;
    /** Raw Telegram files the root shows beside the drive's own. */
    rootFiles: () => Promise<DrivePathFile[]>;
    /** Checked after every read; false abandons the walk. */
    current: () => boolean;
}

// Each segment costs one read of the drive, so a runaway paste stops here.
const MAX_PATH_LENGTH = 4096;
const MAX_SEGMENTS = 256;
const BIDI_EDGES = /^[\u200e\u200f\u202a-\u202e\u2066-\u2069\ufeff]+|[\u200e\u200f\u202a-\u202e\u2066-\u2069\ufeff]+$/g;

function stripQuotes(value: string): string {
    // File Explorer's "Copy as path" wraps the path in double quotes.
    const quoted = /^(["'])([\s\S]*)\1$/.exec(value);
    return quoted ? quoted[2].trim() : value;
}

/** Reads a typed or pasted path into the folder names it walks through. */
export function parseDrivePath(text: string): ParsedDrivePath | DrivePathFailure {
    // A paste can carry several lines; the path is the first one with text.
    const line = text.split(/\r\n|\r|\n/).map((part) => part.trim()).find((part) => part !== '') ?? '';
    // Checked before the marks are stripped, which slows on a huge pasted run of them.
    if (line.length > MAX_PATH_LENGTH * 2) return { kind: 'too-long' };
    // Windows puts an invisible direction mark before paths copied from a
    // Properties or Security tab; it is never part of the path.
    const unmarked = (part: string) => part.replace(BIDI_EDGES, '').trim();
    const value = unmarked(stripQuotes(unmarked(line)));
    if (!value) return { kind: 'empty' };
    if (value.length > MAX_PATH_LENGTH) return { kind: 'too-long' };
    if (/^file:\/\//i.test(value)) return { kind: 'local' };
    if (/^[a-z][a-z0-9+.-]*:\/\//i.test(value)) return { kind: 'link' };
    // Drive names can never hold a colon, so C:\... and \\server\... are paths
    // on this computer rather than in the drive.
    if (/^[a-z]:([\\/]|$)/i.test(value) || value.startsWith('\\\\')) return { kind: 'local' };

    const segments: string[] = [];
    // Backslashes separate too: names cannot contain either slash.
    for (const part of value.split(/[\\/]/)) {
        // A name never ends in a space but may start with one, so only the end
        // is trimmed here and matching retries without the leading space. That
        // also reads search's "My Drive / Docs / Taxes" locations.
        const segment = part.trimEnd();
        const bare = segment.trim();
        if (bare === '' || bare === '.') continue;
        if (bare === '..') {
            segments.pop();
            continue;
        }
        segments.push(segment);
    }
    if (segments.length > MAX_SEGMENTS) return { kind: 'too-long' };
    const parsed: ParsedDrivePath = { segments, folderOnly: segments.length > 0 && /[\\/]\s*$/.test(value) };
    if (looksLikeComputerPath(value, segments.length)) parsed.localHint = true;
    return parsed;
}

// Top folders of macOS and Linux disks in their exact case, a per-user runtime
// folder (/run/user/1000, where GVfs mounts), and the Git Bash and Cygwin forms
// of a Windows path. Names a drive is likely to hold too (Applications,
// Library, System) are left out.
const COMPUTER_ROOTS = /^[\\/](?:Users|Volumes|home|root|private|mnt|usr|var|etc|tmp|opt|run[\\/]user[\\/]\d+|cygdrive[\\/][a-zA-Z]|[a-z][\\/](?:Users|Program Files|ProgramData|Windows))(?:[\\/]|$)/;
const COMPUTER_MEDIA = /^[\\/](?:run[\\/])?media[\\/]/;
// ~, ~/x, ~user/x, $HOME, ${HOME}, $env:NAME and %NAME%, each as a whole segment.
const HOME_SHORTHAND = /^(?:~|~[A-Za-z_][\w.-]*(?=[\\/])|\$HOME|\$\{HOME\}|\$env:[A-Za-z_]\w*|%[A-Za-z_]\w*%)(?:[\\/]|$)/;

/** True for text shaped like a path copied from this computer, minus a drive letter. */
function looksLikeComputerPath(value: string, segmentCount: number): boolean {
    if (HOME_SHORTHAND.test(value)) return true;
    if (COMPUTER_ROOTS.test(value)) return segmentCount >= 2;
    return COMPUTER_MEDIA.test(value) && segmentCount >= 3;
}

function looseKey(name: string): string {
    return name.normalize('NFC').toLowerCase();
}

/**
 * The entries a typed name means: an exact match, else ones that differ only
 * in case or Unicode form. The drive keeps sibling names unique under that
 * looser comparison, so more than one result only comes from raw Telegram
 * files or a name this approximation folds differently.
 */
export function matchName<T extends { name: string }>(entries: readonly T[], typed: string): T[] {
    const candidates = typed === typed.trimStart() ? [typed] : [typed, typed.trimStart()];
    for (const candidate of candidates) {
        const exact = candidate.normalize('NFC');
        const found = entries.filter((entry) => entry.name.normalize('NFC') === exact);
        if (found.length) return found;
    }
    for (const candidate of candidates) {
        const key = looseKey(candidate);
        const found = entries.filter((entry) => looseKey(entry.name) === key);
        if (found.length) return found;
    }
    return [];
}

async function walk(
    segments: string[],
    folderOnly: boolean,
    source: DrivePathSource,
): Promise<DrivePathTarget | DrivePathFailure | null> {
    const path: DrivePathFolder[] = [];
    const where = () => path.map((folder) => folder.name);
    for (let index = 0; index < segments.length; index += 1) {
        const segment = segments[index];
        const last = index === segments.length - 1;
        const parentId = path[path.length - 1]?.id ?? '';
        const listing = await source.list(parentId);
        if (!source.current()) return null;

        const folders = matchName(listing.folders, segment);
        if (folders.length > 1) return { kind: 'ambiguous', name: segment.trim(), parent: where() };
        if (folders.length === 1) {
            path.push({ id: folders[0].id, name: folders[0].name });
            continue;
        }

        let candidates = listing.files;
        if (last && parentId === '') {
            // The root also shows raw Telegram files; a name shared with a drive
            // file has to be reported, not settled in the drive file's favour.
            const managed = new Set(listing.files.map((file) => file.msgId));
            const raw = await source.rootFiles();
            if (!source.current()) return null;
            candidates = [...listing.files, ...raw.filter((file) => !managed.has(file.msgId))];
        }
        const files = matchName(candidates, segment);
        if (files.length > 1) return { kind: 'ambiguous', name: segment.trim(), parent: where() };
        if (files.length === 1) {
            if (!last || folderOnly) return { kind: 'not-a-folder', name: files[0].name, parent: where() };
            return { kind: 'file', path, fileId: String(files[0].msgId) };
        }
        return { kind: 'not-found', name: segment.trim(), parent: where() };
    }
    return { kind: 'folder', path };
}

/**
 * Walks a parsed path from the drive root. A leading drive name ("My Drive",
 * or the drive's own title) is read as the root, unless the root holds an
 * item of that name. Resolves to null once `source.current()` turns false.
 */
export async function resolveDrivePath(
    parsed: ParsedDrivePath,
    rootNames: readonly string[],
    source: DrivePathSource,
): Promise<DrivePathTarget | DrivePathFailure | null> {
    const result = await walk(parsed.segments, parsed.folderOnly, source);
    const first = parsed.segments[0];
    const missedAtRoot = result !== null && (result.kind === 'not-found' || result.kind === 'not-a-folder')
        && result.parent.length === 0;
    if (!missedAtRoot || first === undefined || matchName(rootNames.map((name) => ({ name })), first).length === 0) {
        // "/Users/me/Taxes" missing from the drive is most likely a path on
        // this computer; say so, without claiming it, since the guess can be wrong.
        if (parsed.localHint && result !== null && result.kind === 'not-found' && result.parent.length === 0) {
            return { ...result, maybeLocal: true };
        }
        return result;
    }
    const rest = parsed.segments.slice(1);
    return walk(rest, parsed.folderOnly && rest.length > 0, source);
}

/** The text the path field shows for a folder path. */
export function formatDrivePath(names: readonly string[]): string {
    return '/' + names.join('/');
}

/** The sentence shown under the path field when a path cannot be opened. */
export function describeDrivePathFailure(failure: DrivePathFailure, rootLabel: string): string {
    const example = 'Type a path such as /Documents/Taxes.';
    switch (failure.kind) {
        case 'empty':
            return example;
        case 'local':
            return 'That is a path on this computer, so it cannot be opened here. ' + example;
        case 'link':
            return 'That is a link, not a folder path. ' + example;
        case 'too-long':
            return 'That path is too long.';
        default: {
            const where = failure.parent.length ? formatDrivePath(failure.parent) : rootLabel;
            if (failure.kind === 'not-a-folder') return `"${failure.name}" in ${where} is a file, not a folder.`;
            if (failure.kind === 'ambiguous') return `More than one item in ${where} is named "${failure.name}".`;
            const missing = `"${failure.name}" was not found in ${where}.`;
            if (failure.kind === 'not-found' && failure.maybeLocal) {
                return `${missing} If that is a path on this computer, it cannot be opened here. ${example}`;
            }
            return missing;
        }
    }
}
