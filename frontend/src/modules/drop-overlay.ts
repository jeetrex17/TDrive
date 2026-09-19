// Frosted drop target shown over the file list while an OS file drag is over
// the window. The overlay itself is drawn by ui/transfers/DropOverlay.svelte;
// this module is the controller that decides when it is up, what it says, and
// which box it covers.

import { state } from '../state';
import {
    resetDropOverlayState,
    setDropOverlayState,
    type DropOverlayBox,
} from '../ui/transfers/drop-overlay-store';

// dragover keeps firing while the pointer moves; going quiet for this long is
// the reliable "left the window" signal. dragleave is noisy across children.
const HIDE_AFTER_IDLE_MS = 160;
const FADE_MS = 180;

let listEl: HTMLElement | null = null;
let hideTimer: ReturnType<typeof setTimeout> | null = null;
let fadeTimer: ReturnType<typeof setTimeout> | null = null;
let layoutFrame: number | null = null;
let visible = false;
let title = '';
let box: DropOverlayBox | null = null;
let disposeOverlay: (() => void) | null = null;

// What the store was last told, held as loose fields rather than as a copy of
// the state object so that comparing against it costs nothing.
let sentPresent = false;
let sentVisible = false;
let sentTitle = '';
let sentBox: DropOverlayBox | null = null;

function isFileDrag(event: DragEvent) {
    const types = event.dataTransfer?.types;
    return Boolean(types && Array.from(types).includes('Files'));
}

function targetName() {
    const folder = state.folderPath[state.folderPath.length - 1];
    return folder?.name || state.activeChannel?.title || 'this drive';
}

/**
 * Hands the overlay's state to the component, if any of it has actually moved.
 *
 * `dragover` fires on every pointer move for as long as the drag is over the
 * window, and each one of those calls show(). During a steady drag nothing here
 * changes -- the overlay is up, over the same list, naming the same folder --
 * so the guard is what keeps a drag across the window from allocating a state
 * object and waking Svelte's scheduler once per mouse move. Only the frame that
 * re-measures, and the moments the overlay opens or closes, get through.
 */
function publish(present: boolean): void {
    if (present === sentPresent
        && visible === sentVisible
        && title === sentTitle
        && box === sentBox) return;
    sentPresent = present;
    sentVisible = visible;
    sentTitle = title;
    sentBox = box;
    setDropOverlayState({ present, visible, title, box });
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

// measure reads the list's current box; a zero-size list means it is hidden
// behind another view, so there is nothing to drop on.
function measure(): DropOverlayBox | null {
    if (!listEl) return null;
    const rect = listEl.getBoundingClientRect();
    if (rect.width < 1 || rect.height < 1) return null;
    return { top: rect.top, left: rect.left, width: rect.width, height: rect.height };
}

function scheduleLayout() {
    if (layoutFrame !== null) return;
    layoutFrame = requestFrame(() => {
        layoutFrame = null;
        if (!visible) return;
        const next = measure();
        if (!next) {
            hide();
            return;
        }
        // The new box and the class that fades the card in are published in one
        // update on purpose. Nothing paints between them, so the fade runs from
        // the old opacity at the new position, instead of the card appearing at
        // last frame's position and sliding into place.
        box = next;
        publish(true);
    });
}

function show() {
    if (!listEl) return;
    if (fadeTimer !== null) {
        clearTimeout(fadeTimer);
        fadeTimer = null;
    }
    title = 'Drop to add to ' + targetName();
    visible = true;
    // The element has to be in the layout for a frame before the class that
    // fades it in arrives, or the transition has no starting value to run from
    // and the overlay just appears. Publishing here and again from the frame
    // below is what keeps those two steps in separate paints.
    publish(true);
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
    if (!visible) {
        publish(false);
        return;
    }
    visible = false;
    // Still in the layout: the element has to stay where it is for the opacity
    // transition to have something to fade, and only leaves once it is done.
    publish(true);
    if (fadeTimer !== null) clearTimeout(fadeTimer);
    fadeTimer = setTimeout(() => {
        fadeTimer = null;
        if (!visible) publish(false);
    }, FADE_MS);
}

export function teardownDropOverlay(): void {
    disposeOverlay?.();
}

export function activateDropOverlay(): () => void {
    teardownDropOverlay();
    listEl = document.getElementById('file-list');
    // The list is the one element this module still reaches for, and only ever
    // to read its box. The overlay is fixed-position and deliberately not a
    // child of the list -- the list scrolls and the overlay must not, and a
    // child would be clipped by the scroller -- so where it goes is a
    // measurement rather than something CSS can state. No list, no box to
    // cover, nothing to do.
    if (!listEl) return () => {};

    const onDragOver = (event: DragEvent) => {
        if (!isFileDrag(event) || state.dragState || !state.activeChannel || state.virtualView !== null) return;
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
        title = '';
        box = null;
        sentPresent = false;
        sentVisible = false;
        sentTitle = '';
        sentBox = null;
        resetDropOverlayState();
        listEl = null;
        disposeOverlay = null;
    };
    return disposeOverlay;
}
