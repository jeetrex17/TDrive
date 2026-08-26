import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const globalCss = readFileSync(new URL('../../style.css', import.meta.url), 'utf8');

describe('theme transition motion', () => {
    it('uses a relaxed but still responsive reveal cadence', () => {
        expect(globalCss).toContain('theme-old-fade 300ms');
        expect(globalCss).toContain('theme-new-reveal 480ms');
        expect(globalCss).toContain('theme-app-settle 340ms');
    });
});
