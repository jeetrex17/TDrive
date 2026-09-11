import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ImageExif } from '../exif';

const exif = vi.hoisted(() => ({
    dataUrlToBytes: vi.fn(() => new Uint8Array([0xff, 0xd8, 0xff, 0xd9])),
    parseExif: vi.fn<() => ImageExif>(() => ({ gps: { lat: 37.7749, lon: -122.4194 } })),
}));

vi.mock('../exif', () => ({
    dataUrlToBytes: exif.dataUrlToBytes,
    parseExif: exif.parseExif,
}));

import { renderImageInfoHTML } from './preview-info';
import { state } from '../../state';

beforeEach(() => {
    vi.clearAllMocks();
    exif.dataUrlToBytes.mockReturnValue(new Uint8Array([0xff, 0xd8, 0xff, 0xd9]));
    exif.parseExif.mockReturnValue({ gps: { lat: 37.7749, lon: -122.4194 } });
});

afterEach(() => {
    state.activeChannel = null;
    state.userNames.clear();
});

describe('renderImageInfoHTML location privacy', () => {
    it('keeps coordinates local until the user explicitly opens an external map', () => {
        const host = document.createElement('div');
        host.innerHTML = renderImageInfoHTML({
            item: {
                name: 'photo.jpg',
                size: 1024,
                encrypted: false,
                uploaderId: 0,
                uploadTime: 0,
            },
            fullSrc: 'data:image/jpeg;base64,/9j/2Q==',
            naturalWidth: 0,
            naturalHeight: 0,
        });

        const location = Array.from(host.querySelectorAll<HTMLElement>('.info-section'))
            .find((section) => section.querySelector('.info-section-title')?.textContent === 'Location');
        expect(location?.querySelector('.info-label')?.textContent).toBe('Coordinates');
        expect(location?.querySelector('.info-value')?.textContent).toBe('37.7749, -122.4194');

        expect(location?.querySelector('iframe, embed, object, [src], [srcset]')).toBeNull();

        const action = location?.querySelector<HTMLButtonElement>('button.info-map-link[data-map-url]');
        expect(action?.type).toBe('button');
        expect(action?.textContent).toBe('Open externally in Google Maps');
        expect(action?.getAttribute('aria-label')).toBe('Open this location externally in Google Maps');

        const mapUrl = new URL(action?.dataset.mapUrl ?? '');
        expect(mapUrl.origin).toBe('https://www.google.com');
        expect(mapUrl.pathname).toBe('/maps/search/');
        expect(mapUrl.searchParams.get('api')).toBe('1');
        expect(mapUrl.searchParams.get('query')).toBe('37.7749,-122.4194');
    });
    it('omits all location traffic when the image has no GPS metadata', () => {
        exif.parseExif.mockReturnValue({
            make: '<img src=x onerror=alert(1)>',
            model: 'Camera',
            lens: '<script>alert(1)</script>',
            fNumber: 2.8,
            exposureTime: '1/60',
            focalLength: 35,
            iso: 100,
        });
        state.activeChannel = { id: 1, title: 'Shared', kind: 'shared' };
        state.userNames.set('7', '<img src=x onerror=alert(2)>');

        const host = document.createElement('div');
        host.innerHTML = renderImageInfoHTML({
            item: {
                name: 'photo.jpg',
                size: 2048,
                encrypted: true,
                uploaderId: 7,
                uploadTime: 1_700_000_000,
            },
            fullSrc: 'data:image/jpeg;base64,/9j/2Q==',
            naturalWidth: 4000,
            naturalHeight: 3000,
        });

        expect(host.querySelector('.info-map-link')).toBeNull();
        expect(host.querySelector('img, script')).toBeNull();
        expect(host.textContent).toContain('<img src=x onerror=alert(1)> Camera');
        expect(host.textContent).toContain('<script>alert(1)</script>');
        expect(host.textContent).toContain('<img src=x onerror=alert(2)>');
        expect(host.textContent).toContain('4000 × 3000 · 12 MP');
        expect(host.textContent).toContain('EncryptedYes');
    });

    it('renders an empty state without requesting EXIF for an unloaded image', () => {
        const host = document.createElement('div');
        host.innerHTML = renderImageInfoHTML({
            item: {},
            fullSrc: '',
            naturalWidth: 0,
            naturalHeight: 0,
        });

        expect(host.querySelector('.info-empty')?.textContent).toBe('No details available.');
        expect(exif.dataUrlToBytes).not.toHaveBeenCalled();
        expect(exif.parseExif).not.toHaveBeenCalled();
    });
});
