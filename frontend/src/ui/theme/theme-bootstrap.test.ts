import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';
import { describe, expect, it } from 'vitest';
import { THEME_STORAGE_KEY } from './theme-controller';
import { THEME_DEFINITIONS } from './theme-model';

const html = readFileSync(new URL('../../../index.html', import.meta.url), 'utf8');
const bootstrap = /<script>([\s\S]*?)<\/script>/.exec(html)?.[1];

function runBootstrap(saved: string | null): Record<string, string> {
    if (!bootstrap) throw new Error('missing pre-paint appearance bootstrap');
    const dataset: Record<string, string> = {};

    runInNewContext(bootstrap, {
        document: { documentElement: { dataset } },
        localStorage: { getItem: () => saved },
    });

    return dataset;
}

describe('pre-paint theme bootstrap', () => {
    it('runs before the theme stylesheet to avoid a startup flash', () => {
        expect(html.indexOf('<script>')).toBeGreaterThan(0);
        expect(html.indexOf('<script>')).toBeLessThan(html.indexOf('<link rel="stylesheet"'));
    });

    it('keeps the synchronous whitelist aligned with the typed catalogue', () => {
        expect(bootstrap).toContain(`'${THEME_STORAGE_KEY}'`);
        for (const theme of THEME_DEFINITIONS) {
            expect(bootstrap).toContain(`'${theme.id}'`);
        }
    });

    it('migrates a saved System pair to its explicit dark palette before application boot', () => {
        const saved = JSON.stringify({
            mode: 'system',
            lightThemeId: 'catppuccin-latte',
            darkThemeId: 'nord',
        });

        expect(runBootstrap(saved)).toEqual({ theme: 'nord', themeAppearance: 'dark' });
    });

    it('normalizes missing and unknown persisted values to the dark default without OS lookup', () => {
        const invalid = JSON.stringify({ mode: 'sepia', lightThemeId: 'dracula', darkThemeId: 'missing' });

        expect(runBootstrap(null)).toEqual({ theme: 'tokyo-night', themeAppearance: 'dark' });
        expect(runBootstrap(invalid)).toEqual({ theme: 'tokyo-night', themeAppearance: 'dark' });
        expect(bootstrap).not.toContain('matchMedia');
        expect(bootstrap).not.toContain('prefers-color-scheme');
    });
});
