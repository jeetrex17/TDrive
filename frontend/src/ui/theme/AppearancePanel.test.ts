import { describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import AppearancePanel from './AppearancePanel.svelte';

describe('AppearancePanel', () => {
    it('exposes a quiet System, Light, and Dark mode hierarchy', () => {
        const { body } = render(AppearancePanel);

        expect(body).toContain('Appearance');
        expect(body).toContain('System');
        expect(body).toContain('Follow your system');
        expect(body).toContain('Light');
        expect(body).toContain('Dark');
        expect(body).not.toContain('Personalize');
        expect(body).not.toContain('Automatic pair');
        expect(body).not.toContain('System appearance palette');
        expect(body).not.toContain('aria-label="Theme palette"');
    });

    it('uses radio semantics for mode selection', () => {
        const { body } = render(AppearancePanel);

        expect(body).toContain('role="radiogroup"');
        expect(body).toContain('aria-label="Appearance mode"');
        expect(body).toContain('aria-checked="true"');
        expect(body).toContain('aria-live="polite"');
    });
});
