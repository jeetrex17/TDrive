export const PDF_VIEWER_FRAME_SOURCE = 'tdrive-pdf-frame';

export interface PdfFrameLoadedMessage {
    source: typeof PDF_VIEWER_FRAME_SOURCE;
    type: 'loaded';
    pages: number;
}

export interface PdfFrameErrorMessage {
    source: typeof PDF_VIEWER_FRAME_SOURCE;
    type: 'error';
    message: string;
}

export type PdfFrameMessage = PdfFrameLoadedMessage | PdfFrameErrorMessage;

export function pdfViewerFrameSrc(fileURL: string): string {
    const params = new URLSearchParams({ file: fileURL });
    return `/pdf-viewer.html?${params.toString()}`;
}

export function isPdfFrameMessage(value: unknown): value is PdfFrameMessage {
    if (!value || typeof value !== 'object') return false;
    const candidate = value as Partial<PdfFrameMessage>;
    if (candidate.source !== PDF_VIEWER_FRAME_SOURCE) return false;
    if (candidate.type === 'loaded') return typeof candidate.pages === 'number';
    if (candidate.type === 'error') return typeof candidate.message === 'string';
    return false;
}
