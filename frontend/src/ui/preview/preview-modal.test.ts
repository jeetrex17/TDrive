import { describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import PreviewModal from './PreviewModal.svelte';

describe('PreviewModal', () => {
    it('renders the IDs consumed by the preview controller', () => {
        const { body } = render(PreviewModal);

        for (const id of [
            'preview-shell',
            'preview-stage',
            'preview-filename',
            'preview-image',
            'preview-loading',
            'preview-loading-fill',
            'preview-error',
            'preview-close',
            'preview-prev',
            'preview-next',
            'preview-counter',
            'preview-download',
            'preview-info-btn',
            'preview-info',
            'preview-info-body',
            'preview-locked',
            'preview-locked-input',
            'preview-locked-unlock',
            'preview-locked-eye',
        ]) {
            expect(body).toContain(`id="${id}"`);
        }

        expect(body).toContain('preview-eye-show');
        expect(body).toContain('preview-eye-hide');
    });
});
