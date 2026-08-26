import { describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import AppearancePanel from './AppearancePanel.svelte';

describe('AppearancePanel', () => {
    it('exposes system, light, and dark modes with curated theme previews', () => {
        const { body } = render(AppearancePanel, { props: { onBack: () => {} } });

        expect(body).toContain('Appearance');
        expect(body).toContain('Follow your system');
        expect(body).toContain('Light');
        expect(body).toContain('Dark');
        expect(body).toContain('TDrive Light');
        expect(body).toContain('Catppuccin');
        expect(body).toContain('Solarized');
        expect(body).toContain('Gruvbox');
    });

    it('uses radio semantics for mode and palette selection', () => {
        const { body } = render(AppearancePanel, { props: { onBack: () => {} } });

        expect(body).toContain('role="radiogroup"');
        expect(body).toContain('aria-label="Appearance mode"');
        expect(body).toContain('aria-label="Theme palette"');
        expect(body).toContain('aria-checked="true"');
        expect(body).toContain('aria-live="polite"');
    });
});
