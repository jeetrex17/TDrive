import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { THEME_DEFINITIONS } from './theme-model';

const paletteSource = readFileSync(new URL('./theme-palettes.css', import.meta.url), 'utf8');
const requiredTokens = [
    '--color-canvas',
    '--color-surface-0',
    '--color-surface-1',
    '--color-surface-2',
    '--color-surface-3',
    '--color-text',
    '--color-text-soft',
    '--color-text-muted',
    '--color-text-subtle',
    '--color-border',
    '--color-border-soft',
    '--color-accent',
    '--color-accent-hover',
    '--color-accent-strong',
    '--color-on-accent',
    '--color-danger',
    '--color-on-danger',
    '--color-warning',
    '--color-success',
    '--glass-bg',
    '--glass-border',
    '--scrim',
] as const;

const requiredViewerTokens = [
    '--viewer-text',
    '--viewer-text-muted',
    '--viewer-text-subtle',
    '--viewer-surface-0',
    '--viewer-surface-1',
    '--viewer-surface-2',
    '--viewer-control-hover',
    '--viewer-border',
    '--viewer-border-soft',
    '--viewer-accent',
    '--viewer-on-accent',
    '--viewer-danger',
    '--viewer-on-danger',
    '--viewer-overlay-1',
    '--viewer-overlay-2',
    '--viewer-overlay-3',
    '--viewer-scrim',
    '--viewer-shadow',
    '--viewer-focus-ring',
] as const;

function paletteBlock(themeId: string): string {
    const selector = new RegExp(`data-theme=["']${themeId}["']`);
    const match = selector.exec(paletteSource);
    if (!match) throw new Error(`missing palette selector for ${themeId}`);
    const openBrace = paletteSource.indexOf('{', match.index);
    const closeBrace = paletteSource.indexOf('}', openBrace);
    return paletteSource.slice(openBrace + 1, closeBrace);
}

function sharedRootBlock(): string {
    const match = /^:root\s*\{/m.exec(paletteSource);
    if (!match) throw new Error('missing shared :root theme contract');
    const openBrace = paletteSource.indexOf('{', match.index);
    const closeBrace = paletteSource.indexOf('}', openBrace);
    return paletteSource.slice(openBrace + 1, closeBrace);
}

function tokenValue(block: string, token: string): string {
    const escaped = token.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    const match = new RegExp(`${escaped}\\s*:\\s*([^;]+)`).exec(block);
    return match?.[1].trim() ?? '';
}

function luminance(hex: string): number {
    const channels = hex
        .slice(1)
        .match(/.{2}/g)
        ?.map((value) => Number.parseInt(value, 16) / 255) ?? [];
    const linear = channels.map((channel) =>
        channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4,
    );
    return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2];
}

function contrast(foreground: string, background: string): number {
    const lighter = Math.max(luminance(foreground), luminance(background));
    const darker = Math.min(luminance(foreground), luminance(background));
    return (lighter + 0.05) / (darker + 0.05);
}

function expectReadablePair(
    block: string,
    label: string,
    foregroundToken: string,
    backgroundToken: string,
    minimum = 4.5,
): void {
    const foreground = tokenValue(block, foregroundToken);
    const background = tokenValue(block, backgroundToken);
    expect(foreground, `${label} ${foregroundToken} must be an auditable six-digit hex color`).toMatch(
        /^#[\da-f]{6}$/i,
    );
    expect(background, `${label} ${backgroundToken} must be an auditable six-digit hex color`).toMatch(
        /^#[\da-f]{6}$/i,
    );
    expect(
        contrast(foreground, background),
        `${label} ${foregroundToken} on ${backgroundToken}`,
    ).toBeGreaterThanOrEqual(minimum);
}

describe('theme palettes', () => {
    it('uses Quiet Relay as the deterministic pre-controller fallback', () => {
        expect(paletteSource).toContain(':root:not([data-theme]),');
        expect(paletteSource).toContain(':root[data-theme="quiet-relay"] {');
        expect(paletteSource).not.toContain('prefers-color-scheme');
    });

    it('defines the complete semantic color contract for every theme', () => {
        for (const theme of THEME_DEFINITIONS) {
            const block = paletteBlock(theme.id);
            for (const token of requiredTokens) {
                expect(tokenValue(block, token), `${theme.id} is missing ${token}`).not.toBe('');
            }
        }
    });

    // glass.css builds the popover's readability floor out of this token
    // precisely because it has no alpha of its own to multiply against: one
    // percentage of it means the same opacity in all 17 palettes. Giving it an
    // rgba() value would silently thin every frosted panel by that alpha.
    it('keeps --color-surface-0 fully opaque, which the glass fill depends on', () => {
        for (const theme of THEME_DEFINITIONS) {
            expect(
                tokenValue(paletteBlock(theme.id), '--color-surface-0'),
                `${theme.id} --color-surface-0 must be an opaque six-digit hex color`,
            ).toMatch(/^#[\da-f]{6}$/i);
        }
    });

    it('keeps primary text WCAG AA readable against the app canvas', () => {
        for (const theme of THEME_DEFINITIONS) {
            const block = paletteBlock(theme.id);
            const canvas = tokenValue(block, '--color-canvas');
            const text = tokenValue(block, '--color-text');

            expect(canvas, `${theme.id} canvas must use an auditable six-digit hex color`).toMatch(/^#[\da-f]{6}$/i);
            expect(text, `${theme.id} text must use an auditable six-digit hex color`).toMatch(/^#[\da-f]{6}$/i);
            expect(contrast(text, canvas), `${theme.id} primary text contrast`).toBeGreaterThanOrEqual(4.5);
        }
    });

    it('keeps compact text and action labels WCAG AA readable', () => {
        for (const theme of THEME_DEFINITIONS) {
            const block = paletteBlock(theme.id);
            const pairs = [
                ['--color-text-muted', '--color-canvas'],
                ['--color-text', '--color-surface-0'],
                ['--color-text', '--color-surface-1'],
                ['--color-text', '--color-surface-2'],
                ['--color-text-muted', '--color-surface-0'],
                ['--color-text-muted', '--color-surface-1'],
                ['--color-text-muted', '--color-surface-2'],
                ['--color-text-subtle', '--color-canvas'],
                ['--color-text-subtle', '--color-surface-0'],
                ['--color-text-subtle', '--color-surface-1'],
                ['--color-text-subtle', '--color-surface-2'],
                ['--color-warning', '--color-surface-1'],
                ['--color-success', '--color-surface-1'],
                ['--color-on-accent', '--color-accent'],
                ['--color-on-danger', '--color-danger'],
            ] as const;

            for (const [foregroundToken, backgroundToken] of pairs) {
                expectReadablePair(block, theme.id, foregroundToken, backgroundToken);
            }
        }
    });

    it('keeps the active, focus, and progress accent distinguishable as a UI indicator', () => {
        for (const theme of THEME_DEFINITIONS) {
            expectReadablePair(
                paletteBlock(theme.id),
                theme.id,
                '--color-accent',
                '--color-surface-1',
                3,
            );
        }
    });

    it('defines one fixed-dark semantic contract for content viewers', () => {
        const block = sharedRootBlock();

        for (const token of requiredViewerTokens) {
            expect(tokenValue(block, token), `viewer contract is missing ${token}`).not.toBe('');
        }
    });

    it('keeps fixed-dark viewer text, controls, and statuses WCAG AA readable', () => {
        const block = sharedRootBlock();
        const pairs = [
            ['--viewer-text', '--viewer-surface-0'],
            ['--viewer-text', '--viewer-surface-1'],
            ['--viewer-text', '--viewer-surface-2'],
            ['--viewer-text-muted', '--viewer-surface-0'],
            ['--viewer-text-muted', '--viewer-surface-1'],
            ['--viewer-text-muted', '--viewer-surface-2'],
            ['--viewer-text-subtle', '--viewer-surface-0'],
            ['--viewer-text-subtle', '--viewer-surface-1'],
            ['--viewer-text-subtle', '--viewer-surface-2'],
            ['--viewer-text', '--viewer-control-hover'],
            ['--viewer-text-muted', '--viewer-control-hover'],
            ['--viewer-accent', '--viewer-surface-2'],
            ['--viewer-danger', '--viewer-surface-2'],
            ['--viewer-on-accent', '--viewer-accent'],
            ['--viewer-on-danger', '--viewer-danger'],
        ] as const;

        for (const [foregroundToken, backgroundToken] of pairs) {
            expectReadablePair(block, 'fixed viewer', foregroundToken, backgroundToken);
        }

        expectReadablePair(
            block,
            'fixed viewer control boundary',
            '--viewer-border',
            '--viewer-surface-2',
            3,
        );
    });

    it('keeps selector previews synchronized with the applied palette', () => {
        for (const theme of THEME_DEFINITIONS) {
            const block = paletteBlock(theme.id);
            expect(theme.preview).toEqual([
                tokenValue(block, '--color-canvas'),
                tokenValue(block, '--color-surface-2'),
                tokenValue(block, '--color-accent'),
                tokenValue(block, '--color-text'),
            ]);
        }
    });

    it('maps the reviewed Quiet Relay anchors onto the existing semantic contract', () => {
        const vault = paletteBlock('quiet-relay');
        const day = paletteBlock('daybreak');

        expect([
            tokenValue(vault, '--color-canvas'),
            tokenValue(vault, '--color-surface-0'),
            tokenValue(vault, '--color-surface-1'),
            tokenValue(vault, '--color-text'),
            tokenValue(vault, '--color-accent'),
        ]).toEqual(['#0e171c', '#121e24', '#18272f', '#e7f0f2', '#2aabee']);
        expect([
            tokenValue(day, '--color-canvas'),
            tokenValue(day, '--color-surface-0'),
            tokenValue(day, '--color-surface-1'),
            tokenValue(day, '--color-text'),
            tokenValue(day, '--color-accent'),
        ]).toEqual(['#eef4f5', '#f8fbfb', '#ffffff', '#17262c', '#1689be']);

        expect(tokenValue(vault, '--focus-ring')).toContain('42, 171, 238');
        expect(tokenValue(day, '--focus-ring')).toContain('22, 137, 190');
        for (const block of [vault, day]) {
            const accent = tokenValue(block, '--color-accent');
            expect([
                tokenValue(block, '--color-success'),
                tokenValue(block, '--color-warning'),
                tokenValue(block, '--color-danger'),
            ]).not.toContain(accent);
        }
    });

    it('preserves the existing Porcelain and Tokyo Night foundations', () => {
        const tdriveLight = paletteBlock('porcelain');
        const tokyoNight = paletteBlock('tokyo-night');

        expect(tokenValue(tdriveLight, '--color-canvas')).toBe('#f3f5f9');
        expect(tokenValue(tdriveLight, '--color-surface-0')).toBe('#ffffff');
        expect(tokenValue(tdriveLight, '--color-accent')).toBe('#315fc4');
        expect(tokenValue(tokyoNight, '--color-canvas')).toBe('#1a1b26');
        expect(tokenValue(tokyoNight, '--color-surface-0')).toBe('#16161e');
        expect(tokenValue(tokyoNight, '--color-accent')).toBe('#7aa2f7');
    });
});
