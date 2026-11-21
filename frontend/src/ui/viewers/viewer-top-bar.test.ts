import { describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import PreviewModal from '../preview/PreviewModal.svelte';
import VideoModal from '../video/VideoModal.svelte';
import FileViewerModal from './FileViewerModal.svelte';
import { closeFileViewerView, openFileViewerView } from './file-viewer-store';

function closeButton(body: string): string {
    return body.match(/<button[^>]*class="viewer-icon-btn viewer-close"[^>]*>/)?.[0] ?? '';
}

describe('viewer top bar', () => {
    it('gives the photo, video and document viewers the same close button', () => {
        const photo = render(PreviewModal).body;
        const video = render(VideoModal).body;
        openFileViewerView({ kind: 'pdf', token: 'tok', title: 'Report.pdf', meta: '', mimeType: 'application/pdf', url: '', loading: false, error: '' });
        const document = render(FileViewerModal, { props: { onClose: () => {}, onDownload: () => {} } }).body;
        closeFileViewerView();

        expect(closeButton(photo)).toContain('id="preview-close"');
        expect(closeButton(video)).toContain('id="video-close"');
        expect(closeButton(document)).toContain('aria-label="Close file"');
        for (const body of [photo, video, document]) {
            expect(body).toMatch(/class="viewer-topbar [^"]+"/);
        }
        expect(document).toContain('Report.pdf');
    });
});
