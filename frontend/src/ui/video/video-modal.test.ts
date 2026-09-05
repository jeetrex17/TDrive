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
            'video-audio-wrap',
            'video-audio-button',
            'video-audio-label',
            'video-audio-menu',
            'video-subtitle-wrap',
            'video-subtitle-button',
            'video-subtitle-label',
            'video-subtitle-menu',
            'video-speed-button',
            'video-speed-menu',
            'video-picture-button',
            'video-aspect-button',
            'video-settings-panel',
            'video-settings-close',
            'video-picture-settings',
            'video-subtitle-settings',
            'video-subtitle-size',
            'video-subtitle-color',
            'video-subtitle-outline',
            'video-subtitle-background',
            'video-subtitle-save',
            'video-subtitle-reset',
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

    it('renders quick cycle buttons with only the gear controlling the settings panel', () => {
        const { body } = render(VideoModal);

        expect(body).toContain('id="video-audio-wrap" class="video-menu-wrap video-track-wrap" hidden');
        expect(body).toContain('id="video-subtitle-wrap" class="video-menu-wrap video-track-wrap" hidden');
        for (const id of ['video-audio-button', 'video-subtitle-button', 'video-speed-button', 'video-aspect-button']) {
            expect(body).not.toMatch(new RegExp(`id="${id}"[^>]*aria-(controls|expanded)=`));
        }
        expect(body).toMatch(/id="video-audio-button"[^>]*aria-label="Audio track"/);
        expect(body).toMatch(/id="video-subtitle-button"[^>]*aria-label="Subtitles"/);
        expect(body).toMatch(/id="video-audio-menu"[^>]*role="group"/);
        expect(body).toMatch(/id="video-subtitle-menu"[^>]*role="group"/);
        expect(body).toMatch(/id="video-picture-button"[^>]*aria-label="Playback settings"/);
        expect(body).toMatch(/id="video-picture-button"[^>]*aria-expanded="false"[^>]*aria-controls="video-settings-panel"/);
    });
});
