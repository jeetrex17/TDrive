import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const globalCss = readFileSync(new URL('../../style.css', import.meta.url), 'utf8');

describe('theme transition motion', () => {
    it('uses a deliberate one-second reveal cadence', () => {
        expect(globalCss).toContain('theme-old-fade 600ms');
        expect(globalCss).toContain('theme-new-reveal 1000ms');
        expect(globalCss).toContain('theme-app-settle 1000ms');
    });
});
