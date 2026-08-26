import { describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import VideoModal from './VideoModal.svelte';

describe('VideoModal', () => {
    it('renders the IDs consumed by the video controller', () => {
        const { body } = render(VideoModal);

        for (const id of [
            'video-shell',
            'video-stage',
            'video-native-viewport',
            'video-player',
            'video-filename',
            'video-meta',
            'video-close',
            'video-center-controls',
            'video-center-play',
            'video-center-skip-back',
            'video-center-skip-forward',
            'video-skip-feedback',
            'video-loading',
            'video-loading-status',
            'video-error',
            'video-time',
            'video-scrubber',
            'video-scrubber-buffered',
            'video-scrubber-played',
            'video-scrubber-thumb',
            'video-scrubber-tooltip',
            'video-scrubber-tooltip-image',
            'video-scrubber-tooltip-time',
            'video-duration',
            'video-play',
            'video-skip-back',
            'video-skip-forward',
            'video-mute',
            'video-volume-slider',
            'video-volume-fill',
            'video-volume-thumb',
            'video-track-controls',
            'video-audio-control',
            'video-audio-select',
            'video-subtitle-control',
            'video-subtitle-select',
            'video-speed-button',
            'video-speed-menu',
            'video-fullscreen',
        ]) {
            expect(body).toContain(`id="${id}"`);
        }
    });

    it('announces buffering and fallback status updates atomically', () => {
        const { body } = render(VideoModal);

        expect(body).toContain('id="video-loading-status"');
        expect(body).toContain('role="status"');
        expect(body).toContain('aria-live="polite"');
        expect(body).toContain('aria-atomic="true"');
    });

    it('renders labelled native track selectors hidden by default', () => {
        const { body } = render(VideoModal);

        expect(body).toContain('<label id="video-audio-control"');
        expect(body).toContain('for="video-audio-select"');
        expect(body).toContain('>Audio</span>');
        expect(body).toContain('aria-label="Audio track"');
        expect(body).toContain('<label id="video-subtitle-control"');
        expect(body).toContain('for="video-subtitle-select"');
        expect(body).toContain('>Subtitles</span>');
        expect(body).toContain('aria-label="Subtitle track"');
        expect(body).toContain('id="video-track-controls" class="video-track-controls" aria-label="Media tracks" hidden');
    });
});
