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

function paletteBlock(themeId: string): string {
    const selector = new RegExp(`data-theme=["']${themeId}["']`);
    const match = selector.exec(paletteSource);
    if (!match) throw new Error(`missing palette selector for ${themeId}`);
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

describe('theme palettes', () => {
    it('defines the complete semantic color contract for every theme', () => {
        for (const theme of THEME_DEFINITIONS) {
            const block = paletteBlock(theme.id);
            for (const token of requiredTokens) {
                expect(tokenValue(block, token), `${theme.id} is missing ${token}`).not.toBe('');
            }
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
                ['--color-on-accent', '--color-accent'],
                ['--color-on-danger', '--color-danger'],
            ] as const;

            for (const [foregroundToken, backgroundToken] of pairs) {
                const foreground = tokenValue(block, foregroundToken);
                const background = tokenValue(block, backgroundToken);
                expect(
                    contrast(foreground, background),
                    `${theme.id} ${foregroundToken} on ${backgroundToken}`,
                ).toBeGreaterThanOrEqual(4.5);
            }
        }
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

    it('preserves the existing Tokyo Night foundation', () => {
        const tokyoNight = paletteBlock('tokyo-night');

        expect(tokenValue(tokyoNight, '--color-canvas')).toBe('#1a1b26');
        expect(tokenValue(tokyoNight, '--color-surface-0')).toBe('#16161e');
        expect(tokenValue(tokyoNight, '--color-accent')).toBe('#7aa2f7');
    });
});
