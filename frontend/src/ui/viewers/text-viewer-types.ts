import { fileExtension } from '../../modules/media-types';

export type TextViewerMode = 'plain' | 'markdown' | 'code';
export type StructuredTextLanguage = 'ini' | 'json' | 'plaintext' | 'xml' | 'yaml';

const MARKDOWN_EXTENSIONS = new Set(['md', 'markdown']);
const CODE_LANGUAGES = new Map<string, StructuredTextLanguage>([
    ['cfg', 'ini'],
    ['conf', 'ini'],
    ['ini', 'ini'],
    ['json', 'json'],
    ['yaml', 'yaml'],
    ['yml', 'yaml'],
    ['toml', 'ini'],
    ['xml', 'xml'],
]);

export function textViewerModeForName(name: string): TextViewerMode {
    const ext = fileExtension(name);
    if (MARKDOWN_EXTENSIONS.has(ext)) return 'markdown';
    if (CODE_LANGUAGES.has(ext)) return 'code';
    return 'plain';
}

export function structuredTextLanguageForName(name: string): StructuredTextLanguage | null {
    return CODE_LANGUAGES.get(fileExtension(name)) ?? null;
}
