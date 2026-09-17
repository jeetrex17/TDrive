/**
 * The preview modal's elements, resolved once from its host.
 *
 * The controller used to hold twenty-five `let ...El: any` module variables,
 * each filled by its own `document.getElementById` and each cleared by hand in
 * teardown. Which of them setup refuses to start without lived in a separate
 * array of id strings, so the set of required elements existed twice and the
 * two copies were free to drift: an id could be listed as required and never
 * read, or read on every frame and missing from the list, in which case setup
 * would happily complete and the first write would throw against `null`.
 *
 * Here the answer comes back as one object, and the required/optional split is
 * the type rather than a list to keep in sync. Either every required element is
 * present and the caller gets non-null fields for all of them, or the caller
 * gets the names of the ones that are missing and does not start. Teardown is a
 * single assignment, so half a set can never survive it.
 *
 * Optional fields are genuinely optional: the rendition test renders the stage
 * and its chrome without the info panel or the unlock card, and the controller
 * is expected to go on working without them.
 */

/**
 * Elements setup refuses to start without, because everything writes to them.
 * The modal itself is not listed: it is the host the caller already holds.
 */
const REQUIRED_SELECTORS = {
    shell: '#preview-shell',
    stage: '#preview-stage',
    filename: '#preview-filename',
    image: '#preview-image',
    loading: '#preview-loading',
    loadingFill: '#preview-loading-fill',
    error: '#preview-error',
    closeButton: '#preview-close',
} as const;

export interface PreviewElements {
    readonly modal: HTMLElement;
    readonly shell: HTMLElement;
    readonly stage: HTMLElement;
    readonly filename: HTMLElement;
    readonly image: HTMLImageElement;
    readonly loading: HTMLElement;
    readonly loadingFill: HTMLElement;
    readonly error: HTMLElement;
    readonly closeButton: HTMLElement;

    readonly prevButton: HTMLButtonElement | null;
    readonly nextButton: HTMLButtonElement | null;
    readonly counter: HTMLElement | null;
    readonly downloadButton: HTMLButtonElement | null;
    readonly infoButton: HTMLButtonElement | null;
    readonly infoPanel: HTMLElement | null;
    readonly infoBody: HTMLElement | null;
    readonly infoCloseButton: HTMLButtonElement | null;
    readonly locked: HTMLElement | null;
    readonly lockedInput: HTMLInputElement | null;
    readonly lockedUnlockButton: HTMLButtonElement | null;
    readonly lockedEyeButton: HTMLButtonElement | null;
    readonly lockedError: HTMLElement | null;
    readonly lockedHint: HTMLElement | null;
    readonly lockedHintText: HTMLElement | null;
}

export type PreviewElementsResult =
    | { readonly elements: PreviewElements }
    | { readonly missing: readonly string[] };

/**
 * Resolve the set, or report which required ids the markup does not have.
 *
 * Everything is looked up inside the host rather than through the document, so
 * a stray element that happens to carry a preview id -- a test fixture left in
 * the body, a second copy of the shell mid-swap -- cannot be wired up in place
 * of the one the caller was handed.
 */
export function resolvePreviewElements(host: HTMLElement): PreviewElementsResult {
    const within = <T extends Element>(selector: string): T | null => host.querySelector<T>(selector);

    const missing = Object.values(REQUIRED_SELECTORS)
        .filter((selector) => !within(selector))
        .map((selector) => selector.slice(1));
    if (missing.length) return { missing };

    return {
        elements: {
            modal: host,
            shell: within<HTMLElement>(REQUIRED_SELECTORS.shell)!,
            stage: within<HTMLElement>(REQUIRED_SELECTORS.stage)!,
            filename: within<HTMLElement>(REQUIRED_SELECTORS.filename)!,
            image: within<HTMLImageElement>(REQUIRED_SELECTORS.image)!,
            loading: within<HTMLElement>(REQUIRED_SELECTORS.loading)!,
            loadingFill: within<HTMLElement>(REQUIRED_SELECTORS.loadingFill)!,
            error: within<HTMLElement>(REQUIRED_SELECTORS.error)!,
            closeButton: within<HTMLElement>(REQUIRED_SELECTORS.closeButton)!,

            prevButton: within<HTMLButtonElement>('#preview-prev'),
            nextButton: within<HTMLButtonElement>('#preview-next'),
            counter: within<HTMLElement>('#preview-counter'),
            downloadButton: within<HTMLButtonElement>('#preview-download'),
            infoButton: within<HTMLButtonElement>('#preview-info-btn'),
            infoPanel: within<HTMLElement>('#preview-info'),
            infoBody: within<HTMLElement>('#preview-info-body'),
            infoCloseButton: within<HTMLButtonElement>('#preview-info-close'),
            locked: within<HTMLElement>('#preview-locked'),
            lockedInput: within<HTMLInputElement>('#preview-locked-input'),
            lockedUnlockButton: within<HTMLButtonElement>('#preview-locked-unlock'),
            lockedEyeButton: within<HTMLButtonElement>('#preview-locked-eye'),
            lockedError: within<HTMLElement>('#preview-locked-error'),
            lockedHint: within<HTMLElement>('#preview-locked-hint'),
            lockedHintText: within<HTMLElement>('#preview-locked-hint-text'),
        },
    };
}

/**
 * Whether a resolved set still describes the markup on screen.
 *
 * Svelte can replace the shell under a host that stays put, and the elements we
 * hold would then be detached: every write would land on nodes nobody can see,
 * and the modal would look frozen rather than broken. Asking the elements
 * themselves, rather than asking the document whether something with each id
 * exists, is the difference between "the markup is there" and "the markup we
 * are writing to is there".
 */
export function previewElementsLive(elements: PreviewElements): boolean {
    return elements.modal.isConnected
        && elements.shell.isConnected
        && elements.stage.isConnected
        && elements.filename.isConnected
        && elements.image.isConnected
        && elements.loading.isConnected
        && elements.loadingFill.isConnected
        && elements.error.isConnected
        && elements.closeButton.isConnected;
}
