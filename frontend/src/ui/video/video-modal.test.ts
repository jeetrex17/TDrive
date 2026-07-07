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
            'video-speed-button',
            'video-speed-menu',
            'video-fullscreen',
        ]) {
            expect(body).toContain(`id="${id}"`);
        }
    });
});
