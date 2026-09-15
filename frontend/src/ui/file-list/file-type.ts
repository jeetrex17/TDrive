/*
 * File-type families.
 *
 * A row used to announce its type as uppercase text, which made every file
 * look alike while scanning. Grouping extensions into a handful of families
 * lets a glyph carry the type instead, and keeps the vocabulary small enough
 * that the shapes stay tellable apart at list size.
 *
 * Families are deliberately coarse. A viewer does not need to tell ODT from
 * RTF at list speed; they need to tell a document from a video.
 */

import ArchiveIcon from '@lucide/svelte/icons/archive';
import CodeIcon from '@lucide/svelte/icons/code';
import FileIcon from '@lucide/svelte/icons/file';
import ImageIcon from '@lucide/svelte/icons/image';
import MusicIcon from '@lucide/svelte/icons/music';
import TableIcon from '@lucide/svelte/icons/table';
import TextLinesIcon from '@lucide/svelte/icons/text-align-start';
import VideoIcon from '@lucide/svelte/icons/video';

export type FileTypeFamily =
    | 'document'
    | 'image'
    | 'video'
    | 'audio'
    | 'sheet'
    | 'code'
    | 'archive'
    | 'other';

/** Extensions are compared in the uppercase form splitNameAndExt already produces. */
const EXTENSIONS: Readonly<Record<Exclude<FileTypeFamily, 'other'>, readonly string[]>> = {
    document: ['PDF', 'DOC', 'DOCX', 'ODT', 'RTF', 'TXT', 'MD', 'MARKDN', 'EPUB', 'PAGES', 'TEX'],
    image: ['JPG', 'JPEG', 'PNG', 'GIF', 'WEBP', 'HEIC', 'HEIF', 'AVIF', 'BMP', 'TIFF', 'TIF', 'SVG', 'ICO', 'PSD'],
    video: ['MP4', 'MKV', 'MOV', 'AVI', 'WEBM', 'M4V', 'WMV', 'FLV', 'MPG', 'MPEG', 'M2TS', 'TS', '3GP'],
    audio: ['MP3', 'M4A', 'FLAC', 'WAV', 'AAC', 'OGG', 'OPUS', 'WMA', 'AIFF', 'ALAC', 'MID'],
    sheet: ['XLS', 'XLSX', 'ODS', 'CSV', 'TSV', 'NUMBER'],
    code: [
        'JS', 'MJS', 'CJS', 'TS', 'JSX', 'TSX', 'SVELTE', 'VUE', 'GO', 'PY', 'RS', 'JAVA', 'KT', 'SWIFT',
        'C', 'H', 'CPP', 'HPP', 'CC', 'CS', 'RB', 'PHP', 'SH', 'ZSH', 'BASH', 'LUA', 'SQL',
        'HTML', 'HTM', 'CSS', 'SCSS', 'SASS', 'LESS', 'JSON', 'YAML', 'YML', 'TOML', 'XML', 'INI',
    ],
    archive: ['ZIP', 'RAR', '7Z', 'TAR', 'GZ', 'TGZ', 'BZ2', 'XZ', 'ZST', 'ISO', 'DMG'],
};

const FAMILY_BY_EXTENSION: ReadonlyMap<string, FileTypeFamily> = new Map(
    Object.entries(EXTENSIONS).flatMap(([family, extensions]) =>
        extensions.map((extension) => [extension, family as FileTypeFamily] as const),
    ),
);

/*
 * Content glyphs, not document-wrapper glyphs.
 *
 * Lucide's file-* set draws a page outline, a corner fold and a small badge
 * inside it, which is three to six shapes crammed into the same box a folder
 * fills with one. At list size that reads as a dense blot rather than a type.
 * Naming the content directly costs two or three shapes instead of six.
 *
 * Documents are drawn as lines of text rather than a page, because the page is
 * already what an unrecognised file gets. Lines against a page separates the
 * two far better than a page against a page with lines on it.
 */
const ICONS: Readonly<Record<FileTypeFamily, typeof FileIcon>> = {
    document: TextLinesIcon,
    image: ImageIcon,
    video: VideoIcon,
    audio: MusicIcon,
    sheet: TableIcon,
    code: CodeIcon,
    archive: ArchiveIcon,
    other: FileIcon,
};

/** fileTypeFamily maps one extension onto its family, defaulting to 'other'. */
export function fileTypeFamily(extension: string): FileTypeFamily {
    if (typeof extension !== 'string') return 'other';
    return FAMILY_BY_EXTENSION.get(extension.toUpperCase()) ?? 'other';
}

/** fileTypeIcon returns the glyph a family is drawn with. */
export function fileTypeIcon(family: FileTypeFamily): typeof FileIcon {
    return ICONS[family] ?? FileIcon;
}
