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

describe('global theme style contract', () => {
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
        const appPaletteReferences = viewer.match(appPaletteReference);

        expect(appPaletteReferences ?? []).toEqual([]);
        expect(viewer).toContain('var(--viewer-text)');
        expect(viewer).toContain('var(--viewer-border)');
        expect(viewer).toContain('var(--viewer-surface-0)');
        expect(viewer).toContain('var(--viewer-focus-ring)');
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
