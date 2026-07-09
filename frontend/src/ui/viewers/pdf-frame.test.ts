import { describe, expect, it } from 'vitest';
import { isPdfFrameMessage, pdfViewerFrameSrc } from './pdf-frame';

describe('pdf frame helpers', () => {
    it('encodes the loopback file URL into the frame entrypoint', () => {
        expect(pdfViewerFrameSrc('http://127.0.0.1/media/file/tok?a=1&b=2')).toBe(
            '/pdf-viewer.html?file=http%3A%2F%2F127.0.0.1%2Fmedia%2Ffile%2Ftok%3Fa%3D1%26b%3D2',
        );
    });

    it('accepts only tagged pdf frame messages', () => {
        expect(isPdfFrameMessage({ source: 'tdrive-pdf-frame', type: 'loaded', pages: 4 })).toBe(true);
        expect(isPdfFrameMessage({ source: 'other', type: 'loaded' })).toBe(false);
        expect(isPdfFrameMessage(null)).toBe(false);
    });
});
