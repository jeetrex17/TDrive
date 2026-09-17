/**
 * The typed element boundary for the preview modal.
 *
 * The modal is mounted by Svelte but driven imperatively, so every element it
 * touches is resolved here, once, instead of by scattered id lookups whose
 * results could only be typed as `any`. Collecting in one pass is also what
 * lets activation answer both of its questions -- "is this host still usable?"
 * and "which required elements are missing?" -- from a single traversal.
 *
 * Every field is nullable: the host can be replaced or unmounted between
 * activations, and callers already guard on a missing element rather than
 * assuming the markup is intact.
 */

export interface PreviewDOM {
    modal: HTMLElement | null;
    shell: HTMLElement | null;
    stage: HTMLElement | null;
    filename: HTMLElement | null;
    thumbnail: HTMLImageElement | null;
    image: HTMLImageElement | null;
    loading: HTMLElement | null;
    loadingFill: HTMLElement | null;
    error: HTMLElement | null;
    closeButton: HTMLButtonElement | null;
    prevButton: HTMLButtonElement | null;
    nextButton: HTMLButtonElement | null;
    counter: HTMLElement | null;
    downloadButton: HTMLButtonElement | null;
    infoButton: HTMLButtonElement | null;
    infoPanel: HTMLElement | null;
    infoBody: HTMLElement | null;
    infoCloseButton: HTMLButtonElement | null;
    locked: HTMLElement | null;
    lockedInput: HTMLInputElement | null;
    lockedUnlockButton: HTMLButtonElement | null;
    lockedEyeButton: HTMLButtonElement | null;
    lockedError: HTMLElement | null;
    lockedHint: HTMLElement | null;
    lockedHintText: HTMLElement | null;
}

/**
 * The elements without which the modal cannot open at all. The ids are kept
 * next to their fields because they are what the setup failure reports, and
 * the declaration order is the order those names are listed in.
 */
const REQUIRED_PREVIEW_ELEMENTS: ReadonlyArray<readonly [id: string, key: keyof PreviewDOM]> = [
    ["preview-modal", "modal"],
    ["preview-shell", "shell"],
    ["preview-stage", "stage"],
    ["preview-filename", "filename"],
    ["preview-thumbnail", "thumbnail"],
    ["preview-image", "image"],
    ["preview-loading", "loading"],
    ["preview-loading-fill", "loadingFill"],
    ["preview-error", "error"],
    ["preview-close", "closeButton"],
];

export function byID<T extends HTMLElement>(id: string): T | null {
    return document.getElementById(id) as T | null;
}

export function collectPreviewDOM(): PreviewDOM {
    return {
        modal: byID("preview-modal"),
        shell: byID("preview-shell"),
        stage: byID("preview-stage"),
        filename: byID("preview-filename"),
        thumbnail: byID("preview-thumbnail"),
        image: byID("preview-image"),
        loading: byID("preview-loading"),
        loadingFill: byID("preview-loading-fill"),
        error: byID("preview-error"),
        closeButton: byID("preview-close"),
        prevButton: byID("preview-prev"),
        nextButton: byID("preview-next"),
        counter: byID("preview-counter"),
        downloadButton: byID("preview-download"),
        infoButton: byID("preview-info-btn"),
        infoPanel: byID("preview-info"),
        infoBody: byID("preview-info-body"),
        infoCloseButton: byID("preview-info-close"),
        locked: byID("preview-locked"),
        lockedInput: byID("preview-locked-input"),
        lockedUnlockButton: byID("preview-locked-unlock"),
        lockedEyeButton: byID("preview-locked-eye"),
        lockedError: byID("preview-locked-error"),
        lockedHint: byID("preview-locked-hint"),
        lockedHintText: byID("preview-locked-hint-text"),
    };
}

/** Ids of the required elements this snapshot did not resolve. */
export function missingPreviewElements(dom: PreviewDOM): string[] {
    return REQUIRED_PREVIEW_ELEMENTS.filter(([, key]) => !dom[key]).map(([id]) => id);
}
