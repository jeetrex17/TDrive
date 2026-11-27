import { describe, expect, it, vi } from 'vitest';
import {
    describeDrivePathFailure,
    firstLine,
    formatDrivePath,
    matchName,
    parseDrivePath,
    resolveDrivePath,
    type DriveListing,
    type DrivePathFile,
    type ParsedDrivePath,
} from './drive-path';

function parsed(text: string): ParsedDrivePath {
    const result = parseDrivePath(text);
    if ('kind' in result) throw new Error('expected a path, got ' + result.kind);
    return result;
}

// Team/Projects/2026/Q4 with a Specs folder and a spec file, plus a root file.
const drive: Record<string, DriveListing> = {
    '': {
        folders: [{ id: 'd:team', name: 'Team' }, { id: 'd:spaced', name: ' Spaced' }],
        files: [{ msgId: 7, name: 'readme.txt' }],
    },
    'd:team': { folders: [{ id: 'd:projects', name: 'Projects' }], files: [] },
    'd:projects': { folders: [{ id: 'd:2026', name: '2026' }], files: [] },
    'd:2026': { folders: [{ id: 'd:q4', name: 'Q4' }], files: [] },
    'd:q4': { folders: [{ id: 'd:specs', name: 'Specs' }], files: [{ msgId: 41, name: 'Plan.pdf' }] },
    'd:specs': { folders: [], files: [] },
    'd:spaced': { folders: [], files: [] },
};

function source(listings: Record<string, DriveListing> = drive, rootFiles: DrivePathFile[] = []) {
    return {
        list: vi.fn(async (parentId: string) => listings[parentId] ?? { folders: [], files: [] }),
        rootFiles: vi.fn(async () => rootFiles),
        current: () => true,
    };
}

const roots = ['Personal', 'My Drive'];

describe('parseDrivePath', () => {
    it('splits on either slash and drops empty, repeated and dot segments', () => {
        expect(parsed('/Team//Projects/./2026\\Q4')).toEqual({ segments: ['Team', 'Projects', '2026', 'Q4'], folderOnly: false });
        expect(parsed('Team/Projects/../Projects')).toEqual({ segments: ['Team', 'Projects'], folderOnly: false });
        expect(parsed('/../..')).toEqual({ segments: [], folderOnly: false });
        expect(parsed('/')).toEqual({ segments: [], folderOnly: false });
    });

    it('marks a trailing slash as naming a folder', () => {
        expect(parsed('/Team/Projects/')).toEqual({ segments: ['Team', 'Projects'], folderOnly: true });
        expect(parsed('Team\\ ').folderOnly).toBe(true);
    });

    it('reads a pasted line, quotes and search locations', () => {
        expect(parsed('\n  "/Team/Projects"  \nsecond line').segments).toEqual(['Team', 'Projects']);
        expect(parsed("'Team'").segments).toEqual(['Team']);
        expect(parsed('My Drive / Team / Projects').segments).toEqual(['My Drive', ' Team', ' Projects']);
        // Windows prefixes paths copied from Properties with an invisible direction mark.
        expect(parsed('\u200e /Team/Projects\u200f').segments).toEqual(['Team', 'Projects']);
        expect(parseDrivePath('\u202aC:\\Users\\me')).toEqual({ kind: 'local' });
    });

    it('refuses empty text, links, paths on this computer and runaway pastes', () => {
        expect(parseDrivePath('  \n ')).toEqual({ kind: 'empty' });
        expect(parseDrivePath('""')).toEqual({ kind: 'empty' });
        expect(parseDrivePath('https://t.me/c/1/2')).toEqual({ kind: 'link' });
        expect(parseDrivePath('file:///home/me')).toEqual({ kind: 'local' });
        expect(parseDrivePath('file:/home/me/Docs')).toEqual({ kind: 'local' });
        expect(parseDrivePath('FILE:C:/Users/me')).toEqual({ kind: 'local' });
        expect(parseDrivePath('FILE://server/share')).toEqual({ kind: 'local' });
        expect(parseDrivePath('C:\\Users\\me')).toEqual({ kind: 'local' });
        expect(parseDrivePath('"d:/Photos"')).toEqual({ kind: 'local' });
        expect(parseDrivePath('\\\\server\\share')).toEqual({ kind: 'local' });
        expect(parseDrivePath('/a'.repeat(300))).toEqual({ kind: 'too-long' });
        expect(parseDrivePath('x'.repeat(5000))).toEqual({ kind: 'too-long' });
        // A pasted run of invisible marks is refused before they are stripped.
        const started = Date.now();
        expect(parseDrivePath('\u200e'.repeat(50_000) + 'x')).toEqual({ kind: 'too-long' });
        expect(parseDrivePath('\u200e'.repeat(8000) + '/Team')).toEqual({ segments: ['Team'], folderOnly: false });
        expect(Date.now() - started).toBeLessThan(1000);
    });
});

describe('firstLine', () => {
    it('returns the first line with text, across line endings', () => {
        expect(firstLine('\r\n  \r \n  /Team/Q4  \nnext')).toBe('/Team/Q4');
        expect(firstLine('one\rtwo')).toBe('one');
        expect(firstLine('only')).toBe('only');
        expect(firstLine(' \n\t\r\n')).toBe('');
        expect(firstLine('')).toBe('');
    });

    it('skips uri-list comments only when asked', () => {
        expect(firstLine('# copied\r\nfile:///home/me', true)).toBe('file:///home/me');
        expect(firstLine('# copied\nfile:///home/me')).toBe('# copied');
    });
});

describe('matchName', () => {
    const entries = [{ name: 'Report' }, { name: 'Café' }];

    it('prefers the exact name and falls back to case and Unicode form', () => {
        expect(matchName(entries, 'Report')).toEqual([{ name: 'Report' }]);
        expect(matchName(entries, 'report')).toEqual([{ name: 'Report' }]);
        expect(matchName(entries, 'Cafe\u0301')).toEqual([{ name: 'Café' }]);
        expect(matchName(entries, 'CAFÉ')).toEqual([{ name: 'Café' }]);
        // Decomposed and in another case at once, as a macOS paste can be.
        expect(matchName(entries, 'CAFE\u0301')).toEqual([{ name: 'Café' }]);
        expect(matchName(entries, 'Reports')).toEqual([]);
    });

    it('keeps an exact match over a case-only one', () => {
        expect(matchName([{ name: 'a' }, { name: 'A' }], 'A')).toEqual([{ name: 'A' }]);
        expect(matchName([{ name: 'a' }, { name: 'A' }], 'b')).toEqual([]);
    });

    it('tries a leading space before dropping it', () => {
        expect(matchName([{ name: ' Spaced' }], ' Spaced')).toEqual([{ name: ' Spaced' }]);
        expect(matchName([{ name: 'Team' }], ' Team')).toEqual([{ name: 'Team' }]);
    });
});

describe('resolveDrivePath', () => {
    it('walks one listing per segment and returns the folders it passed', async () => {
        const src = source();
        await expect(resolveDrivePath(parsed('/team/PROJECTS/2026/q4/Specs'), roots, src)).resolves.toEqual({
            kind: 'folder',
            path: [
                { id: 'd:team', name: 'Team' },
                { id: 'd:projects', name: 'Projects' },
                { id: 'd:2026', name: '2026' },
                { id: 'd:q4', name: 'Q4' },
                { id: 'd:specs', name: 'Specs' },
            ],
        });
        expect(src.list.mock.calls.map(([id]) => id)).toEqual(['', 'd:team', 'd:projects', 'd:2026', 'd:q4']);
        expect(src.rootFiles).not.toHaveBeenCalled();
    });

    it('opens a file path at its folder with the file named', async () => {
        await expect(resolveDrivePath(parsed('/Team/Projects/2026/Q4/plan.pdf'), roots, source())).resolves.toEqual({
            kind: 'file',
            path: [
                { id: 'd:team', name: 'Team' },
                { id: 'd:projects', name: 'Projects' },
                { id: 'd:2026', name: '2026' },
                { id: 'd:q4', name: 'Q4' },
            ],
            fileId: '41',
        });
        await expect(resolveDrivePath(parsed('readme.txt'), roots, source())).resolves.toEqual({ kind: 'file', path: [], fileId: '7' });
    });

    it('looks through raw Telegram files only for a file named at the root', async () => {
        const src = source(drive, [{ msgId: 99, name: 'voice.ogg' }]);
        await expect(resolveDrivePath(parsed('/voice.ogg'), roots, src)).resolves.toEqual({ kind: 'file', path: [], fileId: '99' });
        await expect(resolveDrivePath(parsed('/Team/voice.ogg'), roots, src)).resolves.toMatchObject({ kind: 'not-found' });
        expect(src.rootFiles).toHaveBeenCalledTimes(1);
        await expect(resolveDrivePath(parsed('/voice.ogg/'), roots, src)).resolves.toEqual({
            kind: 'not-a-folder', name: 'voice.ogg', parent: [],
        });
    });

    it('names the missing segment and where it was looked for', async () => {
        await expect(resolveDrivePath(parsed('/Team/Projects/2025/Q4'), roots, source())).resolves.toEqual({
            kind: 'not-found', name: '2025', parent: ['Team', 'Projects'],
        });
        await expect(resolveDrivePath(parsed('My Drive / Team / Nope'), roots, source())).resolves.toEqual({
            kind: 'not-found', name: 'Nope', parent: ['Team'],
        });
    });

    it('refuses a file in the middle of a path or before a trailing slash', async () => {
        await expect(resolveDrivePath(parsed('/Team/Projects/2026/Q4/Plan.pdf/x'), roots, source())).resolves.toEqual({
            kind: 'not-a-folder', name: 'Plan.pdf', parent: ['Team', 'Projects', '2026', 'Q4'],
        });
        await expect(resolveDrivePath(parsed('/readme.txt/'), roots, source())).resolves.toMatchObject({ kind: 'not-a-folder' });
    });

    it('reports an ambiguous name instead of picking one', async () => {
        const listings = { '': { folders: [], files: [] } };
        const src = source(listings, [{ msgId: 1, name: 'dup.txt' }, { msgId: 2, name: 'dup.txt' }]);
        await expect(resolveDrivePath(parsed('dup.txt'), roots, src)).resolves.toEqual({ kind: 'ambiguous', name: 'dup.txt', parent: [] });

        // A raw file named like a drive file at the root is a clash too, while
        // the same message listed both ways is one file.
        await expect(resolveDrivePath(parsed('readme.txt'), roots, source(drive, [{ msgId: 8, name: 'readme.txt' }])))
            .resolves.toEqual({ kind: 'ambiguous', name: 'readme.txt', parent: [] });
        await expect(resolveDrivePath(parsed('readme.txt'), roots, source(drive, [{ msgId: 7, name: 'readme.txt' }])))
            .resolves.toEqual({ kind: 'file', path: [], fileId: '7' });
    });

    it('reads a leading drive name as the root unless the root has that folder', async () => {
        const src = source();
        const search = await resolveDrivePath(parsed('My Drive / Team / Projects'), roots, src);
        expect(search).toMatchObject({ kind: 'folder', path: [{ id: 'd:team' }, { id: 'd:projects' }] });
        // The retry from the root reuses the root listing it already read.
        expect(src.list.mock.calls.map(([id]) => id)).toEqual(['', 'd:team']);
        await expect(resolveDrivePath(parsed('/Personal'), roots, source())).resolves.toEqual({ kind: 'folder', path: [] });
        await expect(resolveDrivePath(parsed('/personal/Team/'), roots, source())).resolves.toMatchObject({ kind: 'folder', path: [{ id: 'd:team' }] });

        const named = { ...drive, '': { folders: [{ id: 'd:md', name: 'My Drive' }], files: [] } };
        await expect(resolveDrivePath(parsed('/My Drive'), roots, source(named))).resolves.toEqual({
            kind: 'folder', path: [{ id: 'd:md', name: 'My Drive' }],
        });
        // Inside a real "My Drive" folder, a miss is reported there, not retried from the root.
        await expect(resolveDrivePath(parsed('/My Drive/Team'), roots, source(named))).resolves.toEqual({
            kind: 'not-found', name: 'Team', parent: ['My Drive'],
        });
        await expect(resolveDrivePath(parsed('/Elsewhere/Team'), roots, source())).resolves.toMatchObject({ kind: 'not-found', name: 'Elsewhere' });
    });

    it('matches a leading space in a name before trimming it', async () => {
        await expect(resolveDrivePath(parsed('/ Spaced'), roots, source())).resolves.toEqual({
            kind: 'folder', path: [{ id: 'd:spaced', name: ' Spaced' }],
        });
    });

    it('stops when the caller moves on', async () => {
        let live = true;
        const src = { ...source(), current: () => live };
        src.list.mockImplementation(async (parentId: string) => {
            live = false;
            return drive[parentId];
        });
        await expect(resolveDrivePath(parsed('/Team/Projects'), roots, src)).resolves.toBeNull();
        expect(src.list).toHaveBeenCalledTimes(1);
    });

    it('passes a failed read to the caller', async () => {
        const src = source();
        src.list.mockRejectedValueOnce(new Error('db not ready'));
        await expect(resolveDrivePath(parsed('/Team'), roots, src)).rejects.toThrow('db not ready');
    });
});

describe('describeDrivePathFailure', () => {
    it('says what was missing and where', () => {
        expect(describeDrivePathFailure({ kind: 'not-found', name: 'Q4', parent: ['Team', '2026'] }, 'My Drive'))
            .toBe('"Q4" was not found in /Team/2026.');
        expect(describeDrivePathFailure({ kind: 'not-found', name: 'Q4', parent: [] }, 'Personal'))
            .toBe('"Q4" was not found in Personal.');
        expect(describeDrivePathFailure({ kind: 'not-a-folder', name: 'a.pdf', parent: ['Docs'] }, 'My Drive'))
            .toBe('"a.pdf" in /Docs is a file, not a folder.');
        expect(describeDrivePathFailure({ kind: 'ambiguous', name: 'x', parent: [] }, 'My Drive'))
            .toBe('More than one item in My Drive is named "x".');
        expect(describeDrivePathFailure({ kind: 'local' }, 'My Drive'))
            .toBe('That is a path on this computer, so it cannot be opened here. Type a path such as /Documents/Taxes.');
        expect(describeDrivePathFailure({ kind: 'link' }, 'My Drive')).toContain('link');
        expect(describeDrivePathFailure({ kind: 'empty' }, 'My Drive')).toBe('Type a path such as /Documents/Taxes.');
        expect(describeDrivePathFailure({ kind: 'too-long' }, 'My Drive')).toBe('That path is too long.');
    });

    it('formats a folder path the way the field shows it', () => {
        expect(formatDrivePath([])).toBe('/');
        expect(formatDrivePath(['Team', 'Q4'])).toBe('/Team/Q4');
    });
});
