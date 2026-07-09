import { afterEach, describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import FileViewerModal from './FileViewerModal.svelte';
import { closeFileViewerView, openFileViewerView } from './file-viewer-store';

const noop = () => {};

afterEach(() => {
    closeFileViewerView();
});

describe('FileViewerModal', () => {
    it('renders nothing while closed', () => {
        const { body } = render(FileViewerModal, { props: { onClose: noop, onDownload: noop } });
        expect(body).not.toContain('file-viewer-shell');
    });

    it('renders title and audio controls for audio streams', () => {
        openFileViewerView({
            kind: 'audio',
            token: 'tok',
            url: 'http://127.0.0.1/media/file/tok',
            title: 'song.mp3',
            meta: 'MP3 · 4.0 MB',
            mimeType: 'audio/mpeg',
            loading: false,
            error: '',
        });

        const { body } = render(FileViewerModal, { props: { onClose: noop, onDownload: noop } });

        expect(body).toContain('file-viewer-shell is-audio');
        expect(body).toContain('song.mp3');
        expect(body).toContain('audio-controls');
        expect(body).toContain('aria-label="Seek audio"');
    });

    it('renders text errors as alerts', () => {
        openFileViewerView({
            kind: 'text',
            token: '',
            url: '',
            title: 'notes.txt',
            meta: 'TXT · 2.0 KB',
            mimeType: 'text/plain',
            loading: false,
            error: 'Could not open file',
        });

        const { body } = render(FileViewerModal, { props: { onClose: noop, onDownload: noop } });

        expect(body).toContain('role="alert"');
        expect(body).toContain('Could not open file');
    });
});
