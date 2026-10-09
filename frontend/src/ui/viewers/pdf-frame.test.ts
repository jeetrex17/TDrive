import { describe, expect, it, vi } from 'vitest';
import { acquireScreenProtection } from './view-only-guards';
import { isPdfFrameMessage, pdfViewerFrameSrc } from './pdf-frame';

describe('pdf frame helpers', () => {
    it('encodes the loopback file URL into the frame entrypoint', () => {
        expect(pdfViewerFrameSrc('http://127.0.0.1/media/file/tok?a=1&b=2')).toBe(
            '/pdf-viewer.html?file=http%3A%2F%2F127.0.0.1%2Fmedia%2Ffile%2Ftok%3Fa%3D1%26b%3D2',
        );
    });

    it('marks protected PDFs as view-only inside their frame', () => {
        expect(pdfViewerFrameSrc('http://127.0.0.1/media/file/tok', true)).toContain('&protected=1');
    });

    it('keeps native protection enabled until every protected viewer closes', () => {
        const setProtection = vi.fn();
        const first = acquireScreenProtection(setProtection);
        const second = acquireScreenProtection(setProtection);
        expect(setProtection.mock.calls).toEqual([[true]]);
        first();
        first();
        expect(setProtection.mock.calls).toEqual([[true]]);
        second();
        expect(setProtection.mock.calls).toEqual([[true], [false]]);
    });

    it('accepts only tagged pdf frame messages', () => {
        expect(isPdfFrameMessage({ source: 'tdrive-pdf-frame', type: 'loaded', pages: 4 })).toBe(true);
        expect(isPdfFrameMessage({ source: 'other', type: 'loaded' })).toBe(false);
        expect(isPdfFrameMessage(null)).toBe(false);
    });
});
