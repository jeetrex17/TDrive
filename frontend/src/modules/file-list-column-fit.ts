import { get } from 'svelte/store';
import { fileListView } from '../ui/file-list/file-list-store';
import type { FileListRow } from '../ui/file-list/types';

type Column = 'name' | 'date' | 'size';
type Widths = Partial<Record<Column, number>>;

const STORAGE_KEY = 'tdrive.fileListColumnWidths';
const DEFAULT_WIDTHS: Record<Column, number> = { name: 320, date: 160, size: 110 };
const MIN_WIDTHS: Record<Column, number> = { name: 180, date: 110, size: 72 };

function readWidths(): Widths {
    try {
        const value: unknown = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? '{}');
        if (!value || typeof value !== 'object') return {};
        const widths: Widths = {};
        for (const column of ['name', 'date', 'size'] as const) {
            const width = (value as Record<string, unknown>)[column];
            if (typeof width === 'number' && Number.isFinite(width)) {
                widths[column] = Math.max(MIN_WIDTHS[column], Math.min(width, window.innerWidth * 0.65));
            }
        }
        return widths;
    } catch {
        return {};
    }
}

let widths: Widths = readWidths();

function applyWidths(): void {
    const root = document.querySelector<HTMLElement>('.main-content');
    if (!root) return;
    for (const column of ['name', 'date', 'size'] as const) {
        const width = widths[column];
        if (width !== undefined) root.style.setProperty(`--tdrive-file-col-${column}`, `${width}px`);
    }
}

function textWidth(text: string, font: string): number {
    const canvas = document.createElement('canvas');
    const context = canvas.getContext('2d');
    if (!context) return text.length * 8;
    context.font = font;
    return context.measureText(text).width;
}

function rows(): FileListRow[] {
    const view = get(fileListView);
    return view.kind === 'rows' ? view.rows : [];
}

function measure(column: Column): number {
    const header = document.querySelector<HTMLElement>(column === 'name' ? '.col-name' : column === 'date' ? '.col-date' : '.col-size');
    const sample = header?.querySelector<HTMLElement>('button, span') ?? header;
    const font = sample ? getComputedStyle(sample).font : '14px sans-serif';
    const label = column === 'name' ? 'Name' : column === 'date' ? 'Date' : 'Size';
    let maximum = textWidth(label, font);
    for (const row of rows()) {
        const value = column === 'name' ? row.name : column === 'date'
            ? row.kind === 'pending-folder' ? 'Creating...' : row.metaLabel
            : row.kind === 'pending-folder' ? '…' : row.sizeLabel;
        maximum = Math.max(maximum, textWidth(value ?? '', font));
    }
    // Include the icon, cell padding, inter-item gaps and (for files) uploader chip.
    const extra = column === 'name' ? 104 : column === 'date' ? 40 : 36;
    return Math.ceil(Math.min(maximum + extra, Math.max(MIN_WIDTHS[column], window.innerWidth * 0.65)));
}

export function sizeFileListColumnToFit(column: Column): void {
    widths[column] = measure(column);
    applyWidths();
    try { localStorage.setItem(STORAGE_KEY, JSON.stringify(widths)); } catch { /* preference is optional */ }
}

export function sizeAllFileListColumnsToFit(): void {
    for (const column of ['name', 'date', 'size'] as const) widths[column] = measure(column);
    applyWidths();
    try { localStorage.setItem(STORAGE_KEY, JSON.stringify(widths)); } catch { /* preference is optional */ }
}

export function initializeFileListColumnWidths(): void {
    applyWidths();
}
