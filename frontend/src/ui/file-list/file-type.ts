/*
 * File-type families.
 *
 * A row used to announce its type as uppercase text in a single muted colour,
 * which made every file look alike while scanning. Grouping extensions into a
 * handful of families lets one glyph and one hue carry the type instead, and
 * keeps the vocabulary small enough to stay readable at a glance.
 *
 * Families are deliberately coarse. A viewer does not need to tell ODT from
 * RTF at list speed; they need to tell a document from a video.
 */

import FileIcon from '@lucide/svelte/icons/file';
import FileTextIcon from '@lucide/svelte/icons/file-text';
import FileImageIcon from '@lucide/svelte/icons/file-image';
import FilePlayIcon from '@lucide/svelte/icons/file-play';
import FileHeadphoneIcon from '@lucide/svelte/icons/file-headphone';
import FileSpreadsheetIcon from '@lucide/svelte/icons/file-spreadsheet';
import FileCodeIcon from '@lucide/svelte/icons/file-code';
import FileArchiveIcon from '@lucide/svelte/icons/file-archive';

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

const ICONS: Readonly<Record<FileTypeFamily, typeof FileIcon>> = {
    document: FileTextIcon,
    image: FileImageIcon,
    video: FilePlayIcon,
    audio: FileHeadphoneIcon,
    sheet: FileSpreadsheetIcon,
    code: FileCodeIcon,
    archive: FileArchiveIcon,
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
