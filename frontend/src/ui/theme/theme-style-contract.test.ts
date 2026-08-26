import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const globalCss = readFileSync(new URL('../../style.css', import.meta.url), 'utf8');
const appPaletteReference =
    /var\(--(?:color-|text-main|text-muted|bg-|border\b|accent\b|danger\b|overlay-|focus-ring\b|scrim\b|shadow-)/g;

function sectionBetween(startMarker: string, endMarker: string): string {
    const start = globalCss.indexOf(startMarker);
    const end = globalCss.indexOf(endMarker, start + startMarker.length);
    if (start < 0 || end < 0) throw new Error(`missing CSS section: ${startMarker}`);
    return globalCss.slice(start, end);
}

function ruleFor(selector: string): string {
    const start = globalCss.indexOf(`${selector} {`);
    if (start < 0) throw new Error(`missing CSS rule: ${selector}`);

    const bodyStart = globalCss.indexOf('{', start);
    const bodyEnd = globalCss.indexOf('\n}', bodyStart);
    if (bodyStart < 0 || bodyEnd < 0) throw new Error(`unterminated CSS rule: ${selector}`);

    return globalCss.slice(bodyStart + 1, bodyEnd);
}

describe('global theme style contract', () => {
    it('reveals the native renderer through the themed document canvas', () => {
        expect(globalCss).toMatch(
            /html\.native-video-active,\s*body\.native-video-active\s*\{[^}]*background(?:-color)?:\s*transparent;/,
        );
    });

    it('keeps palette colors out of the lower-specificity bare root', () => {
        const root = /:root\s*\{([\s\S]*?)\n\}/.exec(globalCss)?.[1];
        expect(root).toBeDefined();
        expect(root).not.toMatch(/^\s+--(?:color-|overlay-|glass-|shadow-|focus-ring|scrim)\s*:/m);
        expect(root).toContain('--radius-xs');
        expect(root).toContain('--bg-dark: var(--color-canvas)');
    });

    it('keeps fixed-dark viewer chrome independent from app palette aliases', () => {
        const viewer = sectionBetween(
            '/* ----- File viewers: audio / PDF / text ----- */',
            '/* Profile (avatar) dropdown in the top-right header. */',
        );
        const textPreview = ruleFor('.file-viewer-shell.is-text');
        const fixedViewer = viewer.replace(textPreview, '');
        const appPaletteReferences = fixedViewer.match(appPaletteReference);

        expect(appPaletteReferences ?? []).toEqual([]);
        expect(viewer).toContain('var(--viewer-text)');
        expect(viewer).toContain('var(--viewer-border)');
        expect(viewer).toContain('var(--viewer-surface-0)');
        expect(viewer).toContain('var(--viewer-focus-ring)');
    });

    it('maps text-preview chrome back onto the active app palette', () => {
        const textPreview = ruleFor('.file-viewer-shell.is-text');

        expect(textPreview).toContain('--viewer-text: var(--color-text)');
        expect(textPreview).toContain('--viewer-text-muted: var(--color-text-soft)');
        expect(textPreview).toContain('--viewer-text-subtle: var(--color-text-muted)');
        expect(textPreview).toContain('--viewer-surface-0: var(--color-canvas)');
        expect(textPreview).toContain('--viewer-surface-2: var(--color-surface-1)');
        expect(textPreview).toContain('--viewer-border: var(--color-border)');
        expect(textPreview).toContain('--viewer-accent: var(--color-accent)');
        expect(textPreview).toContain('--viewer-syntax-key: var(--color-accent)');
        expect(textPreview).toContain('--viewer-syntax-string: var(--color-warning)');
        expect(textPreview).toContain('--viewer-syntax-value: var(--color-success)');
    });

    it('keeps fixed-dark preview and video controls independent from app palettes', () => {
        const preview = sectionBetween(
            '/* ----- Lightbox info panel (the "ⓘ" card) ----- */',
            '/* ----- Video player ----- */',
        );
        const video = sectionBetween('/* ----- Video player ----- */', '/* ----- Upload menu (Files / Folder) ----- */');

        expect(preview.match(appPaletteReference) ?? []).toEqual([]);
        expect(video.match(appPaletteReference) ?? []).toEqual([]);
        expect(preview).toContain('var(--viewer-accent)');
        expect(video).toContain('--video-accent: var(--viewer-accent)');
        expect(video).toContain('background: var(--viewer-scrim)');
    });
});
