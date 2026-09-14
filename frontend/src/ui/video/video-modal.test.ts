import { describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import VideoModal from './VideoModal.svelte';

describe('VideoModal', () => {
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
