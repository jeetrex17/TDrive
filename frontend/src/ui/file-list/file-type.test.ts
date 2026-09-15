import { describe, expect, it } from 'vitest';
import { fileTypeFamily, fileTypeIcon } from './file-type';

describe('fileTypeFamily', () => {
    it('groups extensions into families', () => {
        expect(fileTypeFamily('PDF')).toBe('document');
        expect(fileTypeFamily('MKV')).toBe('video');
        expect(fileTypeFamily('FLAC')).toBe('audio');
        expect(fileTypeFamily('HEIC')).toBe('image');
        expect(fileTypeFamily('CSV')).toBe('sheet');
        expect(fileTypeFamily('SVELTE')).toBe('code');
        expect(fileTypeFamily('7Z')).toBe('archive');
    });

    it('accepts the extension in any case', () => {
        // splitNameAndExt uppercases, but callers elsewhere may not.
        expect(fileTypeFamily('mp4')).toBe('video');
        expect(fileTypeFamily('Png')).toBe('image');
    });

    it('falls back to other for unknown and malformed input', () => {
        expect(fileTypeFamily('XYZZY')).toBe('other');
        expect(fileTypeFamily('')).toBe('other');
        expect(fileTypeFamily('FILE')).toBe('other');
        expect(fileTypeFamily(undefined as unknown as string)).toBe('other');
    });

    it('gives every family a distinct glyph, and reuses none across families', () => {
        const families = ['document', 'image', 'video', 'audio', 'sheet', 'code', 'archive', 'other'] as const;
        const icons = families.map((family) => fileTypeIcon(family));
        expect(new Set(icons).size).toBe(families.length);
    });
});
