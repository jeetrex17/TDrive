import { afterEach, describe, expect, it } from 'vitest';
import { NATIVE_VIDEO_LAYER_CLASS, setNativeVideoLayerActive } from './native-video-layer';

afterEach(() => {
    setNativeVideoLayerActive(document, false);
});

describe('native video layer', () => {
    it('clears the root canvas as well as the body while mpv renders underneath', () => {
        setNativeVideoLayerActive(document, true);

        // <html> matters as much as <body>: a themed background on the root
        // element stops body transparency from reaching the page canvas, and
        // the video disappears behind a flat --color-canvas rectangle.
        expect(document.documentElement.classList.contains(NATIVE_VIDEO_LAYER_CLASS)).toBe(true);
        expect(document.body.classList.contains(NATIVE_VIDEO_LAYER_CLASS)).toBe(true);
    });

    it('restores the app backdrop on both elements when playback ends', () => {
        setNativeVideoLayerActive(document, true);
        setNativeVideoLayerActive(document, false);

        expect(document.documentElement.classList.contains(NATIVE_VIDEO_LAYER_CLASS)).toBe(false);
        expect(document.body.classList.contains(NATIVE_VIDEO_LAYER_CLASS)).toBe(false);
    });

    it('is a no-op without a document, so teardown after unmount stays safe', () => {
        expect(() => setNativeVideoLayerActive(undefined, false)).not.toThrow();
    });
});
