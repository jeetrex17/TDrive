import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import FileViewerModal from './FileViewerModal.svelte';
import { closeFileViewerView, openFileViewerView } from './file-viewer-store';

Object.defineProperty(HTMLElement.prototype, 'offsetParent', {
    configurable: true,
    get() {
        return this.parentElement;
    },
});

let host: HTMLElement;
let app: Record<string, unknown> | null = null;

async function settle(): Promise<void> {
    flushSync();
    for (let i = 0; i < 2; i += 1) {
        await Promise.resolve();
        await tick();
        await new Promise((resolve) => { setTimeout(resolve, 0); });
    }
    flushSync();
}

async function waitForElement<T extends Element>(selector: string): Promise<T> {
    for (let i = 0; i < 20; i += 1) {
        await settle();
        const element = host.querySelector<T>(selector);
        if (element) return element;
    }
    throw new Error(`Expected ${selector} to render. DOM: ${host.innerHTML}`);
}

function mountViewer(): void {
    app = mount(FileViewerModal, {
        target: host,
        props: {
            onClose: vi.fn(),
            onDownload: vi.fn(),
        },
    });
}

beforeEach(() => {
    host = document.createElement('div');
    host.id = 'viewer-modal';
    document.body.appendChild(host);
});

afterEach(async () => {
    closeFileViewerView();
    flushSync();
    vi.unstubAllGlobals();
    if (app) await unmount(app);
    host.remove();
    app = null;
});

describe('FileViewerModal text behavior', () => {
    it('renders plain text files after the initial chunk load', async () => {
        vi.stubGlobal('fetch', vi.fn(async () => {
            return new Response('Alpha\nBeta\nGamma\n', {
                status: 206,
                headers: {
                    'Content-Range': 'bytes 0-16/17',
                },
            });
        }));

        openFileViewerView({
            kind: 'text',
            token: 'tok',
            url: 'http://127.0.0.1/media/file/tok',
            title: 'notes.txt',
            meta: 'TXT · 17 B',
            mimeType: 'text/plain; charset=utf-8',
            loading: false,
            error: '',
        });

        mountViewer();
        await settle();

        const body = host.querySelector('.text-viewer-body');
        expect(body?.textContent).toContain('Alpha');
        expect(body?.textContent).toContain('Gamma');
        expect(host.textContent).toContain('End of file');
    });

    it('lets markdown switch between rendered and raw views without refetching', async () => {
        const fetchMock = vi.fn(async () => {
            return new Response('# Hello\n\n**world**\n', {
                status: 206,
                headers: {
                    'Content-Range': 'bytes 0-18/19',
                },
            });
        });
        vi.stubGlobal('fetch', fetchMock);

        openFileViewerView({
            kind: 'text',
            token: 'tok',
            url: 'http://127.0.0.1/media/file/tok',
            title: 'README.md',
            meta: 'MD · 19 B',
            mimeType: 'text/plain; charset=utf-8',
            loading: false,
            error: '',
        });

        mountViewer();

        const heading = await waitForElement('.text-viewer-body.is-markdown h1');
        expect(heading.textContent).toBe('Hello');

        const buttons = host.querySelectorAll<HTMLButtonElement>('.text-viewer-toggle button');
        expect(buttons).toHaveLength(2);
        expect(host.querySelector('.file-viewer-actions .text-viewer-toggle')).not.toBeNull();
        expect(host.querySelector('.text-viewer-footer .text-viewer-toggle')).toBeNull();
        expect(host.querySelector('.text-viewer-footer')?.textContent ?? '').not.toContain('100%');
        expect(host.querySelector('.text-viewer-footer')?.textContent ?? '').not.toContain('Preview ready');
        buttons[1].dispatchEvent(new MouseEvent('click', { bubbles: true }));
        flushSync();

        const rawBody = host.querySelector('pre.text-viewer-body');
        expect(rawBody?.textContent).toContain('# Hello');
        expect(rawBody?.textContent).toContain('**world**');
        expect(fetchMock).toHaveBeenCalledTimes(1);
    });
});
