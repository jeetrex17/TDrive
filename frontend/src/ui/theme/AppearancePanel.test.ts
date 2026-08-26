import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import AppearancePanel from './AppearancePanel.svelte';

const componentSource = readFileSync(new URL('./AppearancePanel.svelte', import.meta.url), 'utf8');
const globalCss = readFileSync(new URL('../../style.css', import.meta.url), 'utf8');

describe('AppearancePanel', () => {
    it('exposes only Light and Dark appearance modes', () => {
        const { body } = render(AppearancePanel);

        expect(body).toContain('Appearance');
        expect(body).toContain('Light');
        expect(body).toContain('Dark');
        expect(body).not.toContain('System');
        expect(body).not.toContain('>Mode<');
        expect(body).not.toContain('Follow your system');
        expect(body).not.toContain('Always bright');
        expect(body).not.toContain('Always dim');
        expect(body).not.toContain('Personalize');
        expect(body).not.toContain('Automatic pair');
        expect(body).toContain('aria-label="Theme palette"');
    });

    it('uses radio semantics for mode selection', () => {
        const { body } = render(AppearancePanel);

        expect(body).toContain('role="radiogroup"');
        expect(body).toContain('aria-label="Appearance mode"');
        expect(body).toContain('aria-checked="true"');
        expect(body).toContain('aria-live="polite"');
    });

    it('keeps the title divider scoped and leaves breathing room above two mode cards', () => {
        expect(globalCss).not.toMatch(/^header\s*\{/m);
        expect(globalCss).toContain('.main-content > header {');
        expect(componentSource).toContain('grid-template-columns: repeat(2, minmax(0, 1fr));');
        expect(componentSource).toContain('.mode-section { padding: 14px 8px 19px; }');
        expect(componentSource).toContain('border-bottom: 1px solid var(--color-border-soft);');
    });
});
