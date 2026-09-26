import { describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import AppearancePanel from './AppearancePanel.svelte';

describe('AppearancePanel', () => {
    it('exposes Light, Dark, and System appearance modes', () => {
        const { body } = render(AppearancePanel);

        expect(body).toContain('Appearance');
        expect(body).toContain('Light');
        expect(body).toContain('Dark');
        expect(body).toContain('System');
        expect(body).not.toContain('>Mode<');
        expect(body).toContain('System');
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
});
