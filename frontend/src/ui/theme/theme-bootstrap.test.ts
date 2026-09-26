import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';
import { describe, expect, it } from 'vitest';
import { THEME_STORAGE_KEY } from './theme-controller';
import { THEME_DEFINITIONS } from './theme-model';

const html = readFileSync(new URL('../../../index.html', import.meta.url), 'utf8');
const bootstrap = /<script>([\s\S]*?)<\/script>/.exec(html)?.[1];

function runBootstrap(saved: string | null, systemDark = false): Record<string, string> {
    if (!bootstrap) throw new Error('missing pre-paint appearance bootstrap');
    const dataset: Record<string, string> = {};

    runInNewContext(bootstrap, {
        document: { documentElement: { dataset } },
        localStorage: { getItem: () => saved },
        matchMedia: () => ({ matches: systemDark }),
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

    it('uses the device appearance for a saved System pair before application boot', () => {
        const saved = JSON.stringify({
            mode: 'system',
            lightThemeId: 'catppuccin-latte',
            darkThemeId: 'nord',
        });

        expect(runBootstrap(saved)).toEqual({ theme: 'catppuccin-latte', themeAppearance: 'light' });
        expect(runBootstrap(saved, true)).toEqual({ theme: 'nord', themeAppearance: 'dark' });
    });

    it('keeps legacy Porcelain and Tokyo Night preferences during pre-paint boot', () => {
        const saved = JSON.stringify({
            mode: 'light',
            lightThemeId: 'porcelain',
            darkThemeId: 'tokyo-night',
        });

        expect(runBootstrap(saved)).toEqual({ theme: 'porcelain', themeAppearance: 'light' });
    });

    it('normalizes missing and unknown persisted values to Quiet Relay', () => {
        const invalid = JSON.stringify({ mode: 'sepia', lightThemeId: 'dracula', darkThemeId: 'missing' });
        expect(runBootstrap(null)).toEqual({ theme: 'quiet-relay', themeAppearance: 'dark' });
        expect(runBootstrap(invalid)).toEqual({ theme: 'quiet-relay', themeAppearance: 'dark' });
        expect(bootstrap).toContain('matchMedia');
        expect(bootstrap).toContain('prefers-color-scheme');
    });
});
