import { describe, expect, it } from 'vitest';
import { renderStructuredText } from './text-viewer';

describe('structured text rendering', () => {
    it('sanitizes rendered markdown', () => {
        const html = renderStructuredText({
            mode: 'markdown',
            source: '# Hello\n\n<script>alert(1)</script>\n[bad](javascript:alert(1))',
        });

        expect(html).toContain('<h1');
        expect(html).toContain('Hello');
        expect(html).not.toContain('<script');
        expect(html).not.toContain('href="javascript:');
    });

    it('renders highlighted structured code with stable classes', () => {
        const html = renderStructuredText({
            mode: 'code',
            language: 'json',
            source: '{"name":"tdrive"}',
        });

        expect(html).toContain('hljs');
        expect(html).toContain('language-json');
        expect(html).toContain('tdrive');
    });
});
