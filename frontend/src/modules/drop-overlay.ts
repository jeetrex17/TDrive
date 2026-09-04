// Frosted drop target shown over the file list while an OS file drag is over
// the window. The overlay never takes pointer events, so the drop still lands
// on the list and the Go-side importer stays the single consumer.

import { state } from '../state';

// dragover keeps firing while the pointer moves; going quiet for this long is
// the reliable "left the window" signal. dragleave is noisy across children.
const HIDE_AFTER_IDLE_MS = 160;
const FADE_MS = 160;

let overlayEl: HTMLElement | null = null;
let titleEl: HTMLElement | null = null;
let listEl: HTMLElement | null = null;
let hideTimer: ReturnType<typeof setTimeout> | null = null;
let visible = false;

function isFileDrag(event: DragEvent) {
    const types = event.dataTransfer?.types;
    return Boolean(types && Array.from(types).includes('Files'));
}

function targetName() {
    const folder = state.folderPath[state.folderPath.length - 1];
    return folder?.name || state.activeChannel?.title || 'this drive';
}

// place covers the list's current box; a zero-size list means it is hidden
// behind another view, so there is nothing to drop on.
function place() {
    if (!overlayEl || !listEl) return false;
    const rect = listEl.getBoundingClientRect();
    if (rect.width < 1 || rect.height < 1) return false;
    overlayEl.style.top = `${rect.top}px`;
    overlayEl.style.left = `${rect.left}px`;
    overlayEl.style.width = `${rect.width}px`;
    overlayEl.style.height = `${rect.height}px`;
    return true;
}

function show() {
    if (!overlayEl || !place()) {
        hide();
        return;
    }
    if (titleEl) titleEl.textContent = `Drop to add to ${targetName()}`;
    if (visible) return;
    visible = true;
    overlayEl.hidden = false;
    requestAnimationFrame(() => {
        if (visible) overlayEl?.classList.add('is-visible');
    });
}

function hide() {
    if (hideTimer) {
        clearTimeout(hideTimer);
        hideTimer = null;
    }
    if (!visible || !overlayEl) return;
    visible = false;
    overlayEl.classList.remove('is-visible');
    const el = overlayEl;
    setTimeout(() => {
        if (!visible) el.hidden = true;
    }, FADE_MS);
}

export function setupDropOverlay() {
    overlayEl = document.getElementById('drop-overlay');
    titleEl = document.getElementById('drop-overlay-title');
    listEl = document.getElementById('file-list');
    if (!overlayEl || !listEl) return;

    window.addEventListener('dragover', (event) => {
        if (!isFileDrag(event) || state.dragState || !state.activeChannel) return;
        show();
        if (hideTimer) clearTimeout(hideTimer);
        hideTimer = setTimeout(hide, HIDE_AFTER_IDLE_MS);
    });
    window.addEventListener('drop', hide);
    window.addEventListener('dragend', hide);
}
