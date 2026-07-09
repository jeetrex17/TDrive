import * as pdfjsLib from 'pdfjs-dist/legacy/build/pdf.mjs';
import workerSrc from 'pdfjs-dist/legacy/build/pdf.worker.mjs?url';
import { PDF_VIEWER_FRAME_SOURCE } from './pdf-frame';
import './pdf-viewer-frame.css';

type PdfViewerModule = typeof import('pdfjs-dist/web/pdf_viewer.mjs');

interface FrameRuntime {
    destroy: () => Promise<void>;
}

const statusEl = document.getElementById('status');
const viewerContainer = document.getElementById('viewerContainer');
const viewerEl = document.getElementById('viewer');

function setStatus(message: string, visible = true): void {
    if (!statusEl) return;
    statusEl.textContent = message;
    statusEl.hidden = !visible;
}

function postFrameMessage(payload: { type: 'loaded'; pages: number } | { type: 'error'; message: string }): void {
    window.parent?.postMessage({ source: PDF_VIEWER_FRAME_SOURCE, ...payload }, '*');
}

function fileURLFromSearch(): string {
    const file = new URLSearchParams(window.location.search).get('file')?.trim() || '';
    if (!file) throw new Error('Missing PDF file URL');
    return file;
}

async function createRuntime(fileURL: string): Promise<FrameRuntime> {
    if (!(viewerContainer instanceof HTMLDivElement) || !(viewerEl instanceof HTMLDivElement)) {
        throw new Error('PDF viewer host is missing');
    }

    pdfjsLib.GlobalWorkerOptions.workerSrc = workerSrc;
    (globalThis as typeof globalThis & { pdfjsLib?: typeof pdfjsLib }).pdfjsLib = pdfjsLib;

    const { EventBus, PDFLinkService, PDFViewer } = (await import('pdfjs-dist/web/pdf_viewer.mjs')) as PdfViewerModule;

    const eventBus = new EventBus();
    const linkService = new PDFLinkService({ eventBus });
    const pdfViewer = new PDFViewer({
        container: viewerContainer,
        viewer: viewerEl,
        eventBus,
        linkService,
        textLayerMode: 1,
        removePageBorders: false,
    });

    linkService.setViewer(pdfViewer);

    const loadingTask = pdfjsLib.getDocument({
        url: fileURL,
        disableRange: false,
        disableStream: false,
        disableAutoFetch: false,
        enableXfa: false,
        rangeChunkSize: 1024 * 1024,
    });
    const pdfDocument = await loadingTask.promise;

    pdfViewer.setDocument(pdfDocument);
    linkService.setDocument(pdfDocument, null);

    const applyPageWidth = (): void => {
        pdfViewer.currentScaleValue = 'page-width';
    };

    let announcedReady = false;
    eventBus.on('pagesinit', () => {
        applyPageWidth();
        if (!announcedReady) {
            announcedReady = true;
            setStatus('', false);
            postFrameMessage({ type: 'loaded', pages: pdfDocument.numPages });
        }
    });

    window.addEventListener('resize', applyPageWidth);

        return {
        destroy: async () => {
            window.removeEventListener('resize', applyPageWidth);
            await pdfDocument.cleanup();
            await loadingTask.destroy();
        },
    };
}

let runtime: FrameRuntime | null = null;

async function boot(): Promise<void> {
    try {
        const fileURL = fileURLFromSearch();
        runtime = await createRuntime(fileURL);
    } catch (error) {
        const message = error instanceof Error ? error.message : 'Could not open PDF';
        setStatus(message, true);
        postFrameMessage({ type: 'error', message });
    }
}

window.addEventListener('pagehide', () => {
    void runtime?.destroy();
    runtime = null;
});

void boot();
