// Frosted drop target shown over the file list while an OS file drag is over
// the window. The overlay never takes pointer events, so the drop still lands
// on the list and the Go-side importer stays the single consumer.

import { state } from '../state';

// dragover keeps firing while the pointer moves; going quiet for this long is
// the reliable "left the window" signal. dragleave is noisy across children.
const HIDE_AFTER_IDLE_MS = 160;
const FADE_MS = 180;

let overlayEl: HTMLElement | null = null;
let titleEl: HTMLElement | null = null;
let listEl: HTMLElement | null = null;
let hideTimer: ReturnType<typeof setTimeout> | null = null;
let fadeTimer: ReturnType<typeof setTimeout> | null = null;
let layoutFrame: number | null = null;
let visible = false;
let disposeOverlay: (() => void) | null = null;

function isFileDrag(event: DragEvent) {
    const types = event.dataTransfer?.types;
    return Boolean(types && Array.from(types).includes('Files'));
}

function targetName() {
    const folder = state.folderPath[state.folderPath.length - 1];
    return folder?.name || state.activeChannel?.title || 'this drive';
}

function requestFrame(callback: () => void): number {
    if (typeof window.requestAnimationFrame === 'function') {
        return window.requestAnimationFrame(callback);
    }
    return window.setTimeout(callback, 0) as unknown as number;
}

function cancelFrame(frame: number): void {
    if (typeof window.cancelAnimationFrame === 'function') {
        window.cancelAnimationFrame(frame);
    } else {
        window.clearTimeout(frame);
    }
}

// place covers the list's current box; a zero-size list means it is hidden
// behind another view, so there is nothing to drop on.
function place() {
    if (!overlayEl || !listEl) return false;
    const rect = listEl.getBoundingClientRect();
    if (rect.width < 1 || rect.height < 1) return false;
    overlayEl.style.top = String(rect.top) + 'px';
    overlayEl.style.left = String(rect.left) + 'px';
    overlayEl.style.width = String(rect.width) + 'px';
    overlayEl.style.height = String(rect.height) + 'px';
    return true;
}

function scheduleLayout() {
    if (layoutFrame !== null) return;
    layoutFrame = requestFrame(() => {
        layoutFrame = null;
        if (!visible || !overlayEl) return;
        if (!place()) {
            hide();
            return;
        }
        overlayEl.classList.add('is-visible');
    });
}

function show() {
    if (!overlayEl || !listEl) return;
    if (fadeTimer !== null) {
        clearTimeout(fadeTimer);
        fadeTimer = null;
    }
    if (titleEl) titleEl.textContent = 'Drop to add to ' + targetName();
    if (!visible) {
        visible = true;
        overlayEl.hidden = false;
    }
    // Layout and class changes are coalesced into one frame for a dragover
    // burst, rather than forcing a layout read for every pointer move.
    scheduleLayout();
}

function hide() {
    if (hideTimer !== null) {
        clearTimeout(hideTimer);
        hideTimer = null;
    }
    if (layoutFrame !== null) {
        cancelFrame(layoutFrame);
        layoutFrame = null;
    }
    if (!overlayEl) return;
    if (!visible) {
        overlayEl.hidden = true;
        return;
    }
    visible = false;
    overlayEl.classList.remove('is-visible');
    const el = overlayEl;
    if (fadeTimer !== null) clearTimeout(fadeTimer);
    fadeTimer = setTimeout(() => {
        fadeTimer = null;
        if (!visible) el.hidden = true;
    }, FADE_MS);
}

export function teardownDropOverlay(): void {
    disposeOverlay?.();
}

export function setupDropOverlay(): (() => void) | undefined {
    teardownDropOverlay();
    overlayEl = document.getElementById('drop-overlay');
    titleEl = document.getElementById('drop-overlay-title');
    listEl = document.getElementById('file-list');
    if (!overlayEl || !listEl) return undefined;

    const onDragOver = (event: DragEvent) => {
        if (!isFileDrag(event) || state.dragState || !state.activeChannel) return;
        show();
        if (hideTimer !== null) clearTimeout(hideTimer);
        hideTimer = setTimeout(hide, HIDE_AFTER_IDLE_MS);
    };
    const onDrop = () => hide();
    const onDragEnd = () => hide();
    const onViewportChange = () => {
        if (visible) scheduleLayout();
    };

    window.addEventListener('dragover', onDragOver);
    window.addEventListener('drop', onDrop);
    window.addEventListener('dragend', onDragEnd);
    window.addEventListener('resize', onViewportChange);
    window.addEventListener('scroll', onViewportChange, true);

    disposeOverlay = () => {
        window.removeEventListener('dragover', onDragOver);
        window.removeEventListener('drop', onDrop);
        window.removeEventListener('dragend', onDragEnd);
        window.removeEventListener('resize', onViewportChange);
        window.removeEventListener('scroll', onViewportChange, true);
        if (hideTimer !== null) clearTimeout(hideTimer);
        if (fadeTimer !== null) clearTimeout(fadeTimer);
        if (layoutFrame !== null) cancelFrame(layoutFrame);
        hideTimer = null;
        fadeTimer = null;
        layoutFrame = null;
        visible = false;
        overlayEl?.classList.remove('is-visible');
        if (overlayEl) overlayEl.hidden = true;
        overlayEl = null;
        titleEl = null;
        listEl = null;
        disposeOverlay = null;
    };
    return disposeOverlay;
}
