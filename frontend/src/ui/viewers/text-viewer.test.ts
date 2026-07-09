import { describe, expect, it } from 'vitest';
import { structuredTextLanguageForName, textViewerModeForName } from './text-viewer';

describe('text viewer mode', () => {
    it('streams plain text-like files', () => {
        expect(textViewerModeForName('notes.txt')).toBe('plain');
        expect(textViewerModeForName('captions.srt')).toBe('plain');
        expect(textViewerModeForName('table.csv')).toBe('plain');
    });

    it('renders markdown separately from plain text', () => {
        expect(textViewerModeForName('README.md')).toBe('markdown');
        expect(textViewerModeForName('guide.markdown')).toBe('markdown');
    });

    it('highlights structured config/code files', () => {
        expect(textViewerModeForName('config.json')).toBe('code');
        expect(textViewerModeForName('docker-compose.yaml')).toBe('code');
        expect(textViewerModeForName('config.toml')).toBe('code');
        expect(textViewerModeForName('schema.xml')).toBe('code');
    });

    it('maps known config formats to explicit highlight languages', () => {
        expect(structuredTextLanguageForName('config.json')).toBe('json');
        expect(structuredTextLanguageForName('config.yml')).toBe('yaml');
        expect(structuredTextLanguageForName('config.toml')).toBe('ini');
        expect(structuredTextLanguageForName('layout.xml')).toBe('xml');
        expect(structuredTextLanguageForName('notes.txt')).toBeNull();
    });
});
