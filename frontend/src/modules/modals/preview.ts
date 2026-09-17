import { closeMedia, isMobilePlatform, openExternalUrl, openOriginalImage, requireOperationSuccess, useEncryptionPassword } from '../../api';
import { state } from '../../state';
import { notify } from '../notifications';
import { humanizeBackendError, isEncryptionPasswordRequired } from '../errors';
import { loadEncryptionStatus } from '../encryption';
import { enqueueDownload } from '../transfers';
import { renderImageInfoHTML } from './preview-info';
import { activateModalOwnership, deactivateModalOwnership, installModalA11y } from '../../ui/modals/modal-a11y';
import { pushSheet, type SheetHandle } from '../../ui/modals/sheet-stack';
import { bindTouchGestures, type TouchGestureHandlers } from '../../ui/preview/touch-gestures';
import { acquireRendition, subscribeRenditionReset, type ImageRequest } from '../renditions/runtime';
import type { RenditionLease } from '../renditions/broker';
import { subscribeGalleryPolicy } from '../gallery-policy';
import { setActive as setGalleryThumbnailScheduling } from '../../ui/gallery/gallery-controller';
import type { FileCommandItem } from '../../ui/file-list/types';
import {
    capturePreviewTransitionSource,
    createPreviewTransitionController,
    type PreviewTransitionSource,
} from './preview-transition';
type PreviewCommandItem = Extract<FileCommandItem, { type: 'file' }>;
type PreviewSelection =
    | { reason: 'none' | 'multiple' | 'unsupported' }
    | { reason: 'ok'; item: PreviewCommandItem; key: string };

// Direct viewing is limited to raster formats whose dimensions and encoded
// bytes the backend can validate before exposing a loopback capability.
const SUPPORTED_EXTENSIONS = new Set(["jpg", "jpeg", "png", "gif", "webp", "bmp"]);

const PREVIEW_CHROME_HIDE_DELAY_MS = 1600;
const REQUIRED_ELEMENT_IDS = [
    "preview-modal",
    "preview-shell",
    "preview-stage",
    "preview-filename",
    "preview-thumbnail",
    "preview-image",
    "preview-loading",
    "preview-loading-fill",
    "preview-error",
    "preview-close",
];

let modalEl: any = null;
let shellEl: any = null;
let stageEl: any = null;
let filenameEl: any = null;
let thumbnailEl: any = null;
let imageEl: any = null;
let loadingEl: any = null;
let loadingFillEl: any = null;
let errorEl: any = null;
let closeBtnEl: any = null;
let prevBtnEl: any = null;
let nextBtnEl: any = null;
let counterEl: any = null;
let downloadBtnEl: any = null;
let infoBtnEl: any = null;
let infoPanelEl: any = null;
let infoBodyEl: any = null;
let infoCloseBtnEl: any = null;
let lockedEl: any = null;
let lockedInputEl: any = null;
let lockedUnlockEl: any = null;
let lockedEyeEl: any = null;
let lockedErrorEl: any = null;
let lockedHintEl: any = null;
let lockedHintTextEl: any = null;
let activeFullSrc = "";
let infoOpen = false;
// On a phone the info card is a sheet, so Android's BACK has to dismiss it
// before the preview under it.
let infoSheetBack: SheetHandle | null = null;

// Zoom/pan state for the displayed image. scale 1 = fit; tx/ty are screen-px
// offsets from center. Reset on navigation and close.
const MAX_ZOOM = 5;
let zoomScale = 1;
let zoomTx = 0;
let zoomTy = 0;
let panning = false;
let panMoved = false;
let panPointerId = -1;
let panStartX = 0;
let panStartY = 0;
// Touch double taps go through the phone recogniser; the dblclick the browser
// synthesises for them must not zoom a second time.
let lastPointerType = "mouse";

// The broker owns thumbnails only. An original image is a short-lived loopback
// session that exists solely for the current explicit viewer action.
let activeThumbnailLease: RenditionLease | null = null;
let activeOriginalSession: { token: string; url: string } | null = null;
let unsubscribePreviewPolicy: (() => void) | null = null;
let unsubscribePreviewReset: (() => void) | null = null;
let previewReady = false;
let previewRequestToken = 0;
let activePreviewKey = "";
let activePreviewItem: any = null;
let chromeHideTimer: any = null;
let previewHostObserver: MutationObserver | null = null;
let previewHostEl: HTMLElement | null = null;
let previewA11y: ReturnType<typeof installModalA11y> | null = null;
const previewListenerCleanups: Array<() => void> = [];
let activePreviewTransitionSource: PreviewTransitionSource | null = null;
const previewTransition = createPreviewTransitionController();

export type PreviewNavigationItem = PreviewCommandItem & {
    channel_id?: number;
    channelId?: number;
    content_revision?: number;
    revision?: number;
    thumbUrl?: string;
    encrypted?: boolean;
    uploaderId?: number;
    uploadTime?: number;
};
export interface PreviewNavigationSource {
    getNeighbor(item: PreviewNavigationItem, direction: -1 | 1): Promise<PreviewNavigationItem | null>;
    getPosition?(item: PreviewNavigationItem): { index: number; total: number } | null;
}

// Gallery supplies a bounded data source. Existing small-list callers keep
// their list adapter; opening a 100k gallery never builds a second full array.
let navSource: PreviewNavigationSource | null = null;
let navigationPending = false;
let navigationEpoch = 0;

function listenPreview(target: EventTarget | null, type: string, listener: EventListener, options?: boolean | AddEventListenerOptions): void {
    if (!target) return;
    target.addEventListener(type, listener, options);
    previewListenerCleanups.push(() => target.removeEventListener(type, listener, options));
}

function isSpaceKey(event: any) {
    return event.code === "Space" || event.key === " " || event.key === "Spacebar";
}

function isTypingContext(element: any) {
    if (!element) return false;
    const tag = String(element.tagName || "").toUpperCase();
    return element.isContentEditable || tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || tag === "BUTTON";
}

function isBlockingOverlayOpen() {
    const overlays = Array.from(document.querySelectorAll(".modal-overlay"));
    if (overlays.some((el: any) => el.id !== "preview-modal" && el.style.display !== "none")) {
        return true;
    }

    return Boolean(document.querySelector("#context-menu .context-menu-panel"));
}

function flashStatus(message: any) {
    if (!message) return;
    notify({ level: 'info', title: message, durationMs: 2400 });
}

function getPreviewKey(item: any) {
	if (!item || item.type !== "file") return "";
	const channelID = Number(item.channel_id || item.channelId || item.ChannelID || state.activeChannel?.id || 0);
	return `file:${channelID}:${Number(item.id || 0)}`;
}

function clearActivePreview() {
    activePreviewKey = "";
    activePreviewItem = null;
    navSource = null;
    navigationPending = false;
    navigationEpoch += 1;
    updateNavChrome();
}

function isPreviewVisible() {
    return Boolean(imageEl && !imageEl.hidden && imageEl.getAttribute("src"));
}

function getSelectedPreviewTarget(): PreviewSelection {
    const items = Array.from(state.selectedItems.values());
    if (items.length === 0) return { reason: 'none' };
    if (items.length > 1) return { reason: 'multiple' };

    const item = items[0];
    if (!item || item.type !== 'file') return { reason: 'unsupported' };
    if (!isPreviewableImage(item.name)) return { reason: 'unsupported' };

    return { reason: 'ok', item, key: getPreviewKey(item) };
}

function clearChromeHideTimer() {
    if (!chromeHideTimer) return;
    clearTimeout(chromeHideTimer);
    chromeHideTimer = null;
}

function setChromeVisible(visible: any) {
    if (!modalEl) return;
    modalEl.classList.toggle("is-chrome-visible", Boolean(visible));
}

function isErrorVisible() {
    return Boolean(errorEl && errorEl.style.display !== "none");
}

function scheduleChromeHide() {
    clearChromeHideTimer();

    if (!isPreviewOpen() || isErrorVisible()) return;
    if (closeBtnEl && closeBtnEl === document.activeElement) return;

    chromeHideTimer = setTimeout(() => {
        if (!isPreviewOpen() || isErrorVisible()) return;
        if (closeBtnEl && closeBtnEl === document.activeElement) return;
        setChromeVisible(false);
    }, PREVIEW_CHROME_HIDE_DELAY_MS);
}

function revealChrome() {
    if (!isPreviewOpen()) return;
    setChromeVisible(true);
    scheduleChromeHide();
}

function resetImageSurface({ keepThumbnail = false } = {}) {
    if (thumbnailEl && !keepThumbnail) {
        thumbnailEl.hidden = true;
        thumbnailEl.removeAttribute("src");
    }
    if (imageEl) {
        imageEl.hidden = true;
        imageEl.removeAttribute("src");
        imageEl.alt = "";
    }
    modalEl?.classList.remove('is-original-ready');
}

function showPreviewLoading(_label?: any) {
    if (!modalEl || !loadingEl) return;
    modalEl.classList.add("is-preview-loading");
    loadingEl.style.display = "flex";
    loadingEl.setAttribute("aria-hidden", "false");
}

function hidePreviewProgress() {
    if (!modalEl || !loadingEl || !loadingFillEl) return;
    modalEl.classList.remove("is-preview-loading");
    loadingEl.style.display = "none";
    loadingEl.setAttribute("aria-hidden", "true");
    loadingFillEl.style.width = "0%";
}

function preparePreviewSurface(filename: any, { keepCurrentImage = false } = {}) {
    if (!modalEl || !filenameEl || !loadingEl || !errorEl) return;
    hideLockedState();
    if (!keepCurrentImage) {
        filenameEl.textContent = filename || "Preview";
    }
    showPreviewLoading();
    errorEl.style.display = "none";
    errorEl.textContent = "";
    modalEl.classList.remove("is-preview-error");

    if (!keepCurrentImage) resetImageSurface();
    if (imageEl && !keepCurrentImage) imageEl.alt = "";
}

function showPreviewError(message: any, { keepCurrentImage = false } = {}) {
    if (!modalEl || !loadingEl || !errorEl) return;

    previewTransition.cancel();
    hideLockedState();

    modalEl.classList.remove("is-preview-locked");
    hidePreviewProgress();
    if (!keepCurrentImage) resetImageSurface();
    errorEl.textContent = message || "Download failed";
    errorEl.style.display = "block";
    modalEl.classList.add("is-preview-error");
    setChromeVisible(true);
    clearChromeHideTimer();
}

function showPreviewImage(src: any, alt: any, { keepLoading = false } = {}) {
    if (!modalEl || !filenameEl || !imageEl || !loadingEl || !errorEl) return;

    hideLockedState();
    modalEl.classList.remove("is-preview-locked");
    if (!keepLoading) {
        hidePreviewProgress();
    } else {
        showPreviewLoading(alt || "Preview");
    }
    errorEl.style.display = "none";
    errorEl.textContent = "";
    modalEl.classList.remove("is-preview-error");
    filenameEl.textContent = alt || "Preview";
    imageEl.alt = alt || 'Preview';
    imageEl.src = src;
    imageEl.hidden = false;
    // The CSS layer transition promotes the original over its thumbnail in
    // 160ms. It intentionally does not touch transform, which zoom and drag
    // own, and it is disabled by the reduced-motion media query.
    revealChrome();
}

/** Pins a thumbnail by its immutable revision while the original stream opens. */
function showPreviewThumbnail(src: string, alt: string): void {
    if (!thumbnailEl) return;
    thumbnailEl.alt = '';
    thumbnailEl.src = src;
    thumbnailEl.hidden = false;
    thumbnailEl.setAttribute('aria-label', `Thumbnail for ${alt}`);
    if (previewTransition.isRunning()) previewTransition.finishOpen(thumbnailEl);
}

function releaseOriginalSession(): void {
    const session = activeOriginalSession;
    activeOriginalSession = null;
    activeFullSrc = '';
    if (session?.token) void Promise.resolve(closeMedia(session.token)).catch(() => {});
}

function isPreviewOpen() {
    return Boolean(modalEl && modalEl.style.display !== "none");
}

function normalizePreviewError(err: any) {
    if (err instanceof Error && err.message.trim()) return err;
    if (typeof err === "string" && err.trim()) return new Error(err.trim());
    if (err && typeof err.message === "string" && err.message.trim()) return new Error(err.message.trim());
    return new Error("Download failed");
}

function showSelectionPreviewError(selection: any) {
    if (selection.reason === "multiple") {
        flashStatus("Preview works with one image at a time");
        return;
    }
    if (selection.reason === "unsupported") {
        flashStatus("Preview is available for image files only");
    }
}

function assertPreviewReady() {
    if (previewReady) return true;
    console.error("Preview modal is unavailable because setup did not complete.");
    flashStatus("Preview unavailable");
    return false;
}

export function isPreviewableImage(filename: any) {
    const name = String(filename || "").trim();
    const dot = name.lastIndexOf(".");
    if (dot < 0 || dot === name.length - 1) return false;
    return SUPPORTED_EXTENSIONS.has(name.slice(dot + 1).toLowerCase());
}

function renditionRequest(target: PreviewNavigationItem): ImageRequest {
    return {
        channelId: Number(target.channel_id || target.channelId || state.activeChannel?.id || 0),
        fileId: Number(target.id),
        revision: Number(target.content_revision || target.revision || 0),
        kind: 'thumbnail',
    };
}

function releasePreviewResources(): void {
    activeThumbnailLease?.release();
    activeThumbnailLease = null;
    releaseOriginalSession();
}

export async function loadPreview(target: PreviewNavigationItem, { keepThumbnail = false } = {}) {
    if (!assertPreviewReady()) throw new Error('Preview unavailable');
    const token = ++previewRequestToken;
    const filename = target.name || 'Preview';
    activePreviewKey = getPreviewKey(target);
    activePreviewItem = target;
    releasePreviewResources();
    resetZoom();
    resetImageSurface({ keepThumbnail });
    updateNavChrome();
    refreshInfoPanel();
    if (target.encrypted && !state.encryption.passwordRemembered) {
        showLockedState();
        return null;
    }
    const request = renditionRequest(target);
    const thumbnailLease = acquireRendition(request, 'viewer');
    activeThumbnailLease = thumbnailLease;
    // Do not trust an old DOM URL as identity. The shared broker either reuses
    // the same revision asset or reacquires it with the immutable request.
    void thumbnailLease.promise.then(asset => {
        if (token !== previewRequestToken || !isPreviewOpen() || activeThumbnailLease !== thumbnailLease) return;
        showPreviewThumbnail(asset.url, filename);
    }).catch(error => {
        if (token !== previewRequestToken || !isPreviewOpen()) return;
        if (isEncryptionPasswordRequired(error)) showLockedState();
    });

    try {
        // This call happens only because opening/navigating the viewer was an
        // explicit action. No original bytes are put in Blob or rendition cache.
        const opened = await openOriginalImage(Number(target.id), request.revision);
        if (token !== previewRequestToken || !isPreviewOpen()) {
            void Promise.resolve(closeMedia(opened.token)).catch(() => {});
            return null;
        }
        activeOriginalSession = { token: opened.token, url: opened.url };
        activeFullSrc = opened.url;
        showPreviewImage(opened.url, filename, { keepLoading: true });
        imageEl.title = 'Original image';
        refreshInfoPanel();
        return { src: opened.url };
    } catch (error) {
        if (token !== previewRequestToken || !isPreviewOpen()) return null;
        if (isEncryptionPasswordRequired(error)) {
            showLockedState();
            return null;
        }
        const normalized = normalizePreviewError(error);
        if (thumbnailEl?.getAttribute('src')) {
            hidePreviewProgress();
            imageEl.title = 'Original image unavailable';
            errorEl.textContent = normalized.message;
            errorEl.style.display = 'block';
        } else showPreviewError(normalized.message);
        return null;
    }
}

export function closePreviewModal() {
    if (zoomScale === 1 && activePreviewTransitionSource && isPreviewVisible()) {
        previewTransition.playClose(activePreviewTransitionSource, imageEl);
    } else {
        previewTransition.cancel();
    }
    activePreviewTransitionSource = null;
    previewRequestToken += 1;
    releasePreviewResources();
    clearActivePreview();
    closeInfoPanel();
    hideLockedState();
    if (lockedInputEl) lockedInputEl.value = "";
    resetZoom();
    activeFullSrc = "";
    clearChromeHideTimer();

    if (modalEl) {
        modalEl.style.display = "none";
        modalEl.setAttribute("aria-hidden", "true");
        previewA11y?.deactivate();
        deactivateModalOwnership(modalEl);
        modalEl.classList.remove("is-chrome-visible", "is-preview-error", "is-preview-locked", "is-shared-entering");
    }
    if (filenameEl) filenameEl.textContent = "";
    if (loadingEl) loadingEl.style.display = "none";
    if (errorEl) {
        errorEl.style.display = "none";
        errorEl.textContent = "";
    }
    hidePreviewProgress();
    resetImageSurface();
    setGalleryThumbnailScheduling(true);
}

// openPreviewItem shows the modal and loads one item. It does not touch the
// navigation context, so both single-item and list callers route through it.
async function openPreviewItem(item: any, transitionSource: PreviewTransitionSource | null = null) {
    const wasOpen = isPreviewOpen();
    const keepCurrentImage = wasOpen && isPreviewVisible();

    previewTransition.cancel();
    activePreviewTransitionSource = transitionSource;
    modalEl.style.display = "flex";
    modalEl.setAttribute("aria-hidden", "false");
    previewA11y?.activate();
    activateModalOwnership(modalEl);
    // The modal owns a pinned thumbnail, so yielding the gallery viewport
    // releases below-the-overlay work and keeps mobile memory predictable.
    setGalleryThumbnailScheduling(false);
    setChromeVisible(true);
    preparePreviewSurface(item.name || "Preview", { keepCurrentImage });
    const sharedTransition = !wasOpen && transitionSource
        ? previewTransition.beginOpen(transitionSource, modalEl)
        : false;
    if (sharedTransition && transitionSource) showPreviewThumbnail(transitionSource.imageSrc, item.name || "Preview");

    try {
        await loadPreview(item, { keepThumbnail: sharedTransition });
        return true;
    } catch {
        return false;
    }
}

export async function openPreviewForSelection(target: PreviewCommandItem | null = null) {
    if (!assertPreviewReady()) return false;

    const selection: PreviewSelection = target
        ? { reason: 'ok', item: target, key: getPreviewKey(target) }
        : getSelectedPreviewTarget();

    if (selection.reason === "none") return false;
    if (selection.reason !== "ok") {
        showSelectionPreviewError(selection);
        return false;
    }

    // Single-item open: no list to page through.
    navSource = null;
    navigationPending = false;
    navigationEpoch += 1;
    updateNavChrome();
    return openPreviewItem(selection.item);
}

function findGalleryPreviewSource(item: any): PreviewTransitionSource | null {
    const id = Number(item?.id || 0);
    if (!id) return null;
    const cell = document.querySelector<HTMLElement>('.gallery-cell[data-id="' + id + '"]');
    return capturePreviewTransitionSource(cell);
}

/** Compatibility adapter for existing small, already-loaded file lists. */
export async function openPreviewList(
    items: any[],
    index: number,
    transitionSource: PreviewTransitionSource | null = null,
) {
    if (!Array.isArray(items) || items.length === 0) return false;
    const i = Math.max(0, Math.min(items.length - 1, Number(index) || 0));
    // Maintain the current index rather than building another index/map of all
    // items. This path is deliberately separate from gallery cursor navigation.
    let position = i;
    const source: PreviewNavigationSource = {
        async getNeighbor(item, direction) {
            if (items[position]?.id !== item.id) return null;
            const next = position + direction;
            return next >= 0 && next < items.length ? items[next] : null;
        },
        getPosition(item) {
            if (items[position]?.id !== item.id) {
                if (items[position + 1]?.id === item.id) position += 1;
                else if (items[position - 1]?.id === item.id) position -= 1;
            }
            return { index: position, total: items.length };
        },
    };
    return openPreviewSource(source, items[i], transitionSource);
}

export async function openPreviewSource(
    source: PreviewNavigationSource,
    item: PreviewNavigationItem,
    transitionSource: PreviewTransitionSource | null = null,
): Promise<boolean> {
    if (!assertPreviewReady()) return false;
    navSource = source;
    navigationEpoch += 1;
    navigationPending = false;
    return openPreviewItem(item, transitionSource || findGalleryPreviewSource(item));
}

function navigationPosition(): { index: number; total: number } | null {
    return activePreviewItem ? navSource?.getPosition?.(activePreviewItem) ?? null : null;
}

function canNavigate(direction: -1 | 1): boolean {
    if (!navSource || navigationPending) return false;
    const position = navigationPosition();
    return !position || (direction < 0 ? position.index > 0 : position.index < position.total - 1);
}

async function navigatePreview(delta: number) {
    const direction = delta < 0 ? -1 : 1;
    if (!isPreviewOpen() || !navSource || !canNavigate(direction)) return;
    const source = navSource;
    const epoch = ++navigationEpoch;
    navigationPending = true;
    updateNavChrome();
    try {
        const item = await source.getNeighbor(activePreviewItem, direction);
        if (!item || epoch !== navigationEpoch || source !== navSource || !isPreviewOpen()) return;
        // Once the neighboring record is known, navigation can interrupt its
        // image transfer. A slow photo must not trap the user on that slide.
        navigationPending = false;
        await openPreviewItem(item, findGalleryPreviewSource(item));
    } catch {
        if (epoch === navigationEpoch && isPreviewOpen()) flashStatus('Could not load the next photo. Try again.');
    } finally {
        if (epoch === navigationEpoch) {
            navigationPending = false;
            updateNavChrome();
        }
    }
}

function updateNavChrome() {
    const position = navigationPosition();
    const hasList = Boolean(navSource && (!position || position.total > 1));
    if (prevBtnEl) {
        prevBtnEl.hidden = !hasList;
        prevBtnEl.disabled = !canNavigate(-1);
    }
    if (nextBtnEl) {
        nextBtnEl.hidden = !hasList;
        nextBtnEl.disabled = !canNavigate(1);
    }
    if (counterEl) {
        counterEl.hidden = !hasList || !position;
        counterEl.textContent = hasList && position ? `${position.index + 1} / ${position.total}` : '';
    }
}

function handleDownloadFromPreview() {
    const id = Number(activePreviewItem?.id || 0);
    if (!id) return;
    const name = String(activePreviewItem?.name || "");
    const size = Number(activePreviewItem?.size || 0);
    enqueueDownload(id, name, size);
}

function toggleInfoPanel() {
    if (infoOpen) closeInfoPanel();
    else openInfoPanel();
}

function openInfoPanel() {
    if (!infoPanelEl || !modalEl) return;
    infoOpen = true;
    // Clear whatever a drag left behind, so the sheet rises from the edge it
    // was thrown to rather than jumping there first.
    infoPanelEl.style.transition = "";
    infoPanelEl.style.transform = "";
    modalEl.classList.add("is-info-open");
    infoBtnEl?.setAttribute("aria-pressed", "true");
    if (isMobilePlatform() && !infoSheetBack) {
        infoSheetBack = pushSheet(() => {
            infoSheetBack = null;
            closeInfoPanel();
        });
    }
    refreshInfoPanel();
}

function closeInfoPanel() {
    infoOpen = false;
    modalEl?.classList.remove("is-info-open");
    infoBtnEl?.setAttribute("aria-pressed", "false");
    infoSheetBack?.release();
    infoSheetBack = null;
}

// Derivative dimensions and stripped EXIF are not original metadata. Keep
// those fields unknown until a metadata source explicitly supplies them.
function refreshInfoPanel() {
    if (!infoOpen || !infoBodyEl || !activePreviewItem) return;
    const hasFull = Boolean(activeFullSrc);
    infoBodyEl.innerHTML = renderImageInfoHTML({
        item: activePreviewItem,
        fullSrc: activeFullSrc,
        naturalWidth: hasFull ? imageEl?.naturalWidth || 0 : 0,
        naturalHeight: hasFull ? imageEl?.naturalHeight || 0 : 0,
    });
}

// --- encrypted "locked" state: an inline unlock card shown in place of the
// image, so navigating onto a locked photo never throws up a modal. ---

function showLockedState() {
    if (!lockedEl) return;
    previewTransition.cancel();
    hidePreviewProgress();
    resetImageSurface();
    if (errorEl) {
        errorEl.style.display = "none";
        errorEl.textContent = "";
    }
    modalEl?.classList.remove("is-preview-error");
    if (lockedErrorEl) {
        lockedErrorEl.style.display = "none";
        lockedErrorEl.textContent = "";
    }
    if (lockedInputEl) lockedInputEl.value = "";
    resetLockedReveal();

    const hint = String(state.encryption?.hint || "").trim();
    if (lockedHintEl && lockedHintTextEl) {
        lockedHintTextEl.textContent = hint;
        lockedHintEl.style.display = hint ? "block" : "none";
    }
    lockedEl.style.display = "flex";
    // Make the backdrop opaque so the gallery behind isn't visible through it.
    modalEl?.classList.add("is-preview-locked");
    setChromeVisible(true);
    clearChromeHideTimer();
    // Intentionally not auto-focusing the field: while browsing, arrow keys
    // should skip past a locked photo, not get captured as typing. The user
    // clicks the field when they actually want to unlock.
}

function hideLockedState() {
    // Only hides the unlock pill; the frosted backdrop class is cleared when an
    // image or error actually takes over, so navigating between two locked
    // photos doesn't flash the gallery during the load in between.
    if (lockedEl) lockedEl.style.display = "none";
}

// Show/hide the typed password. A ghost toggle inside the pill (a polish
// hallmark for password fields); resets to hidden whenever the state reopens.
function toggleLockedReveal() {
    if (!lockedInputEl || !lockedEyeEl) return;
    const reveal = lockedInputEl.type === "password";
    lockedInputEl.type = reveal ? "text" : "password";
    lockedEyeEl.setAttribute("aria-pressed", reveal ? "true" : "false");
    lockedEyeEl.setAttribute("aria-label", reveal ? "Hide password" : "Show password");
    try { lockedInputEl.focus(); } catch { /* focus is best-effort */ }
}

function resetLockedReveal() {
    if (lockedInputEl) lockedInputEl.type = "password";
    if (lockedEyeEl) {
        lockedEyeEl.setAttribute("aria-pressed", "false");
        lockedEyeEl.setAttribute("aria-label", "Show password");
    }
}

function showLockedError(msg: string) {
    if (!lockedErrorEl) return;
    lockedErrorEl.textContent = msg;
    lockedErrorEl.style.display = "block";
}

async function submitInlineUnlock() {
    const value = String(lockedInputEl?.value || "");
    if (!value) {
        showLockedError("Enter your encryption password.");
        return;
    }
    const target = activePreviewItem;
    if (lockedUnlockEl) lockedUnlockEl.disabled = true;
    if (lockedInputEl) lockedInputEl.disabled = true;
    try {
        requireOperationSuccess(await useEncryptionPassword(value));
        await loadEncryptionStatus();
        if (lockedInputEl) lockedInputEl.value = "";
        // Let the gallery's locked thumbnail cells reload too.
        window.dispatchEvent(new Event("tdrive:unlocked"));
        hideLockedState();
        if (target) void loadPreview(target); // re-load the photo, now decryptable
    } catch (err) {
        showLockedError(humanizeBackendError(err));
    } finally {
        if (lockedUnlockEl) lockedUnlockEl.disabled = false;
        if (lockedInputEl) lockedInputEl.disabled = false;
    }
}

// --- zoom / pan ---

function applyZoomTransform() {
    if (!imageEl) return;
    imageEl.style.transform = `translate(${zoomTx}px, ${zoomTy}px) scale(${zoomScale})`;
    imageEl.style.cursor = zoomScale > 1 ? (panning ? "grabbing" : "grab") : "zoom-in";
}

function resetZoom() {
    zoomScale = 1;
    zoomTx = 0;
    zoomTy = 0;
    panning = false;
    panPointerId = -1;
    if (imageEl) {
        imageEl.style.transform = "";
        imageEl.style.cursor = "zoom-in";
    }
}

// displayedImageSize is the painted size of the image inside its element box.
// With object-fit:contain the box can be larger than the picture on one axis,
// so we fit naturalWidth/Height into the box to get the real edges.
function displayedImageSize(): { w: number; h: number } {
    const cw = imageEl?.clientWidth || 0;
    const ch = imageEl?.clientHeight || 0;
    const nw = imageEl?.naturalWidth || 0;
    const nh = imageEl?.naturalHeight || 0;
    if (nw <= 0 || nh <= 0 || cw <= 0 || ch <= 0) return { w: cw, h: ch };
    const fit = Math.min(cw / nw, ch / nh);
    return { w: nw * fit, h: nh * fit };
}

// clampPan keeps the panned image from drifting past its own painted edges.
function clampPan() {
    if (!imageEl) return;
    const { w, h } = displayedImageSize();
    const maxX = Math.max(0, ((zoomScale - 1) * w) / 2);
    const maxY = Math.max(0, ((zoomScale - 1) * h) / 2);
    zoomTx = Math.max(-maxX, Math.min(maxX, zoomTx));
    zoomTy = Math.max(-maxY, Math.min(maxY, zoomTy));
}

// zoomAt scales toward a screen point so the pixel under the cursor stays put.
// The anchor is the cursor's offset from the image's *current* on-screen center
// (getBoundingClientRect already reflects the live transform), which is correct
// regardless of the stage's padding, centering, or the info panel.
function zoomAt(clientX: number, clientY: number, factor: number) {
    if (!imageEl || !isPreviewVisible()) return;
    const next = Math.max(1, Math.min(MAX_ZOOM, zoomScale * factor));
    if (next === zoomScale) return;
    const rect = imageEl.getBoundingClientRect();
    const ax = clientX - (rect.left + rect.width / 2);
    const ay = clientY - (rect.top + rect.height / 2);
    const ratio = next / zoomScale;
    zoomTx += ax * (1 - ratio);
    zoomTy += ay * (1 - ratio);
    zoomScale = next;
    if (zoomScale <= 1.001) {
        zoomScale = 1;
        zoomTx = 0;
        zoomTy = 0;
    }
    clampPan();
    applyZoomTransform();
}

function handleZoomWheel(e: any) {
    if (!isPreviewVisible()) return;
    e.preventDefault();
    zoomAt(e.clientX, e.clientY, e.deltaY < 0 ? 1.18 : 1 / 1.18);
}

function handleZoomDblClick(e: any) {
    if (!isPreviewVisible()) return;
    if (lastPointerType === "touch" && isMobilePlatform()) return;
    e.preventDefault();
    if (zoomScale > 1) resetZoom();
    else zoomAt(e.clientX, e.clientY, 2.5);
}

function handlePanStart(e: any) {
    if (zoomScale <= 1 || !imageEl) return;
    panning = true;
    panMoved = false;
    panPointerId = e.pointerId;
    panStartX = e.clientX - zoomTx;
    panStartY = e.clientY - zoomTy;
    try {
        imageEl.setPointerCapture(e.pointerId);
    } catch {}
    imageEl.style.cursor = "grabbing";
    e.preventDefault();
}

function handlePanMove(e: any) {
    if (!panning || e.pointerId !== panPointerId) return;
    panMoved = true;
    zoomTx = e.clientX - panStartX;
    zoomTy = e.clientY - panStartY;
    clampPan();
    applyZoomTransform();
}

function handlePanEnd(e: any) {
    if (!panning || e.pointerId !== panPointerId) return;
    panning = false;
    panPointerId = -1;
    try {
        imageEl.releasePointerCapture(e.pointerId);
    } catch {}
    if (imageEl) imageEl.style.cursor = zoomScale > 1 ? "grab" : "zoom-in";
}

// --- phone gestures: a tap toggles the chrome, a double tap and a pinch zoom,
// a sideways swipe moves through the set and a swipe down closes. ---

// A second finger turns a pan into a pinch, so the pan lets go of its pointer.
function endPan() {
    if (!panning) return;
    panning = false;
    try {
        imageEl?.releasePointerCapture(panPointerId);
    } catch {}
    panPointerId = -1;
}

function settleDrag(animated: boolean) {
    if (!imageEl || !modalEl) return;
    const from = imageEl.style.transform;
    imageEl.style.transform = "";
    modalEl.style.removeProperty("--preview-dismiss");
    if (!animated || !from || typeof imageEl.animate !== "function") return;
    const reduceMotion = typeof window.matchMedia === "function"
        && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    if (reduceMotion) return;
    imageEl.animate([{ transform: from }, { transform: "none" }], { duration: 180, easing: "cubic-bezier(0.2, 0, 0, 1)" });
}

// The info card is a sheet on a phone, so it goes the way a sheet goes: a tap
// on the picture behind it, a drag down, or BACK. It is a sibling of the stage,
// so none of this touches the stage's own swipe.
const INFO_DISMISS_PX = 96;
const INFO_DISMISS_VELOCITY = 0.6;

function settleInfoSheet(to: string) {
    if (!infoPanelEl) return;
    infoPanelEl.style.transition = "";
    infoPanelEl.style.transform = to;
}

function infoTouchHandlers(): TouchGestureHandlers {
    return {
        dragStart: (axis) => axis === "y" && infoOpen && (infoBodyEl?.scrollTop || 0) <= 0,
        drag: (_dx, dy) => {
            if (!infoPanelEl || (infoBodyEl?.scrollTop || 0) > 0) return;
            infoPanelEl.style.transition = "none";
            infoPanelEl.style.transform = `translate3d(0, ${Math.max(0, dy)}px, 0)`;
        },
        dragEnd: (_dx, dy, _axis, velocity) => {
            if (dy > INFO_DISMISS_PX || (dy > 24 && velocity > INFO_DISMISS_VELOCITY)) {
                // Let the throw finish downwards while the card fades out.
                settleInfoSheet("translateY(100%)");
                closeInfoPanel();
                return;
            }
            settleInfoSheet("");
        },
    };
}

function previewTouchHandlers(): TouchGestureHandlers {
    return {
        tap: () => {
            if (!isPreviewOpen() || !modalEl) return;
            if (infoOpen) {
                closeInfoPanel();
                return;
            }
            clearChromeHideTimer();
            setChromeVisible(!modalEl.classList.contains("is-chrome-visible"));
        },
        doubleTap: (x, y) => {
            if (!isPreviewVisible()) return;
            if (zoomScale > 1) resetZoom();
            else zoomAt(x, y, 2.5);
        },
        pinchStart: endPan,
        pinch: (factor, x, y) => zoomAt(x, y, factor),
        // A zoomed picture pans instead; the pan handlers above own that pointer.
        dragStart: () => zoomScale === 1 && isPreviewVisible() && !modalEl?.classList.contains("is-preview-locked"),
        drag: (dx, dy, axis) => {
            if (!imageEl || !modalEl) return;
            if (axis === "x") {
                imageEl.style.transform = `translate3d(${dx}px, 0, 0)`;
                return;
            }
            const drop = Math.max(0, dy);
            imageEl.style.transform = `translate3d(0, ${drop}px, 0) scale(${Math.max(0.82, 1 - drop / 1400)})`;
            modalEl.style.setProperty("--preview-dismiss", String(Math.min(1, drop / 240)));
        },
        dragEnd: (dx, dy, axis, velocity) => {
            if (axis === "x") {
                const step = dx < 0 ? 1 : -1;
                if ((Math.abs(dx) > 56 || velocity > 0.5) && canNavigate(step)) {
                    settleDrag(false);
                    void navigatePreview(step);
                    return;
                }
                settleDrag(true);
                return;
            }
            if (dy > 96 || (dy > 24 && velocity > 0.6)) {
                settleDrag(false);
                closePreviewModal();
                return;
            }
            settleDrag(true);
        },
    };
}

async function handlePreviewKeydown(event: any) {
    const spacePressed = isSpaceKey(event);
    const previewOpen = isPreviewOpen();

    if (event.key === "Escape" && previewOpen) {
        event.preventDefault();
        event.stopPropagation();
        closePreviewModal();
        return;
    }

    if (previewOpen && (event.key === "ArrowLeft" || event.key === "ArrowRight")) {
        if (!navSource) return;
        if (event.metaKey || event.ctrlKey || event.altKey) return;
        if (isTypingContext(document.activeElement)) return; // e.g. the unlock field
        event.preventDefault();
        event.stopPropagation();
        void navigatePreview(event.key === "ArrowLeft" ? -1 : 1);
        return;
    }

    if (previewOpen && (event.key === "i" || event.key === "I")) {
        if (event.metaKey || event.ctrlKey || event.altKey) return;
        if (isTypingContext(document.activeElement)) return;
        event.preventDefault();
        event.stopPropagation();
        toggleInfoPanel();
        return;
    }

    if (!spacePressed || event.defaultPrevented) return;
    if (event.metaKey || event.ctrlKey || event.altKey) return;
    if (isTypingContext(document.activeElement)) return;
    if (isBlockingOverlayOpen()) return;

    const selection = getSelectedPreviewTarget();

    if (!previewOpen) {
        if (selection.reason !== 'ok') {
            if (selection.reason !== 'none') showSelectionPreviewError(selection);
            return;
        }
        event.preventDefault();
        event.stopPropagation();
        await openPreviewForSelection(selection.item);
        return;
    }

    event.preventDefault();
    event.stopPropagation();

    if (selection.reason !== "ok") {
        showSelectionPreviewError(selection);
        return;
    }

    if (selection.key && selection.key !== activePreviewKey) {
        await openPreviewForSelection(selection.item);
        return;
    }

    closePreviewModal();
}

export function teardownPreviewModal(): void {
    if (isPreviewOpen()) closePreviewModal();
    previewA11y?.deactivate();
    if (modalEl) deactivateModalOwnership(modalEl);
    previewA11y = null;
    for (let i = previewListenerCleanups.length - 1; i >= 0; i -= 1) {
        previewListenerCleanups[i]();
    }
    previewListenerCleanups.length = 0;
    previewHostObserver?.disconnect();
    previewHostObserver = null;
    previewHostEl = null;
    previewReady = false;
    previewTransition.cancel();
    releasePreviewResources();
    unsubscribePreviewPolicy?.();
    unsubscribePreviewPolicy = null;
    unsubscribePreviewReset?.();
    unsubscribePreviewReset = null;
    clearActivePreview();
    infoOpen = false;
    infoSheetBack?.release();
    infoSheetBack = null;
    modalEl = null;
    shellEl = null;
    stageEl = null;
    filenameEl = null;
    imageEl = null;
    loadingEl = null;
    loadingFillEl = null;
    errorEl = null;
    closeBtnEl = null;
    prevBtnEl = null;
    nextBtnEl = null;
    counterEl = null;
    downloadBtnEl = null;
    infoBtnEl = null;
    infoPanelEl = null;
    infoBodyEl = null;
    infoCloseBtnEl = null;
    lockedEl = null;
    lockedInputEl = null;
    lockedUnlockEl = null;
    lockedEyeEl = null;
    lockedErrorEl = null;
    lockedHintEl = null;
    lockedHintTextEl = null;
}

export function activatePreviewModal(): () => void {
    const host = document.getElementById("preview-modal");
    if (!host) {
        if (previewHostEl) teardownPreviewModal();
        return () => {};
    }

    const canReuse = previewHostEl === host
        && previewReady
        && REQUIRED_ELEMENT_IDS.every((id) => Boolean(document.getElementById(id)));
    if (canReuse) {
        updateNavChrome();
        return teardownPreviewModal;
    }
    if (previewHostEl || previewReady) teardownPreviewModal();

    previewHostEl = host;
    previewHostObserver = new MutationObserver(() => {
        if (!host.isConnected) teardownPreviewModal();
    });
    previewHostObserver.observe(document.body, { childList: true, subtree: true });

    const missing = REQUIRED_ELEMENT_IDS.filter((id) => !document.getElementById(id));
    if (missing.length) {
        console.error("Preview modal setup failed. Missing DOM elements: " + missing.join(", "));
        teardownPreviewModal();
        return () => {};
    }

    modalEl = document.getElementById("preview-modal");
    shellEl = document.getElementById("preview-shell");
    stageEl = document.getElementById("preview-stage");
    filenameEl = document.getElementById("preview-filename");
    thumbnailEl = document.getElementById("preview-thumbnail");
    imageEl = document.getElementById("preview-image");
    loadingEl = document.getElementById("preview-loading");
    loadingFillEl = document.getElementById("preview-loading-fill");
    errorEl = document.getElementById("preview-error");
    closeBtnEl = document.getElementById("preview-close");
    prevBtnEl = document.getElementById("preview-prev");
    nextBtnEl = document.getElementById("preview-next");
    counterEl = document.getElementById("preview-counter");
    downloadBtnEl = document.getElementById("preview-download");
    infoBtnEl = document.getElementById("preview-info-btn");
    infoPanelEl = document.getElementById("preview-info");
    infoBodyEl = document.getElementById("preview-info-body");
    infoCloseBtnEl = document.getElementById("preview-info-close");
    lockedEl = document.getElementById("preview-locked");
    lockedInputEl = document.getElementById("preview-locked-input");
    lockedUnlockEl = document.getElementById("preview-locked-unlock");
    lockedEyeEl = document.getElementById("preview-locked-eye");
    lockedErrorEl = document.getElementById("preview-locked-error");
    lockedHintEl = document.getElementById("preview-locked-hint");
    lockedHintTextEl = document.getElementById("preview-locked-hint-text");
    previewReady = true;
    previewA11y = installModalA11y(modalEl, {
        requestClose: closePreviewModal,
        initialFocus: () => closeBtnEl,
        restoreFocus: () => state.virtualView === "photos"
            ? document.getElementById("gallery-view")
            : document.getElementById("file-list"),
    });

    listenPreview(prevBtnEl, "click", ((event: MouseEvent) => {
        event.stopPropagation();
        void navigatePreview(-1);
    }) as EventListener);
    listenPreview(nextBtnEl, "click", ((event: MouseEvent) => {
        event.stopPropagation();
        void navigatePreview(1);
    }) as EventListener);
    listenPreview(downloadBtnEl, "click", ((event: MouseEvent) => {
        event.stopPropagation();
        handleDownloadFromPreview();
    }) as EventListener);
    listenPreview(infoBtnEl, "click", ((event: MouseEvent) => {
        event.stopPropagation();
        toggleInfoPanel();
    }) as EventListener);
    listenPreview(infoCloseBtnEl, "click", ((event: MouseEvent) => {
        event.stopPropagation();
        closeInfoPanel();
    }) as EventListener);
    listenPreview(lockedUnlockEl, "click", ((event: MouseEvent) => {
        event.stopPropagation();
        void submitInlineUnlock();
    }) as EventListener);
    listenPreview(lockedEyeEl, "click", ((event: MouseEvent) => {
        event.stopPropagation();
        toggleLockedReveal();
    }) as EventListener);
    listenPreview(lockedInputEl, "keydown", ((event: KeyboardEvent) => {
        if (event.key === "Enter") {
            event.preventDefault();
            void submitInlineUnlock();
        }
        event.stopPropagation();
    }) as EventListener);
    listenPreview(infoPanelEl, "click", ((event: MouseEvent) => {
        const link = (event.target as HTMLElement).closest("[data-map-url]") as HTMLElement | null;
        if (!link) return;
        event.stopPropagation();
        const url = link.getAttribute("data-map-url") || "";
        if (url) openExternalUrl(url);
    }) as EventListener);
    listenPreview(closeBtnEl, "click", closePreviewModal as EventListener);
    listenPreview(modalEl, "click", ((event: MouseEvent) => {
        if (panMoved) {
            panMoved = false;
            return;
        }
        // On a phone a tap on the picture toggles the chrome; the X and a swipe
        // down close.
        if (isMobilePlatform()) return;
        if (event.target === modalEl || event.target === shellEl || event.target === stageEl) closePreviewModal();
    }) as EventListener);
    listenPreview(modalEl, "pointermove", ((event: PointerEvent) => {
        if (event.pointerType === "touch" && isMobilePlatform()) return;
        if (isPreviewOpen()) revealChrome();
    }) as EventListener);
    if (isMobilePlatform()) {
        listenPreview(stageEl, "pointerdown", ((event: PointerEvent) => {
            lastPointerType = event.pointerType;
        }) as EventListener, true);
        previewListenerCleanups.push(bindTouchGestures(stageEl, previewTouchHandlers()));
        previewListenerCleanups.push(bindTouchGestures(infoPanelEl, infoTouchHandlers()));
    }
    listenPreview(stageEl, "wheel", handleZoomWheel as EventListener, { passive: false });
    listenPreview(imageEl, "dblclick", handleZoomDblClick as EventListener);
    listenPreview(imageEl, "pointerdown", handlePanStart as EventListener);
    listenPreview(imageEl, "pointermove", handlePanMove as EventListener);
    listenPreview(imageEl, "pointerup", handlePanEnd as EventListener);
    listenPreview(imageEl, "pointercancel", handlePanEnd as EventListener);
    listenPreview(closeBtnEl, "focus", (() => {
        setChromeVisible(true);
        clearChromeHideTimer();
    }) as EventListener);
    listenPreview(closeBtnEl, "blur", (() => scheduleChromeHide()) as EventListener);
    listenPreview(imageEl, "error", (() => {
        if (isPreviewOpen() && imageEl?.getAttribute("src")) {
            releaseOriginalSession();
            showPreviewError("Not a supported image", { keepCurrentImage: true });
        }
    }) as EventListener);
    listenPreview(imageEl, "load", (() => {
        modalEl?.classList.add('is-original-ready');
        hidePreviewProgress();
        if (infoOpen) refreshInfoPanel();
    }) as EventListener);
    unsubscribePreviewReset = subscribeRenditionReset(() => {
        if (isPreviewOpen()) closePreviewModal();
    });
    unsubscribePreviewPolicy = subscribeGalleryPolicy(policy => {
        if (policy.backgrounded && isPreviewOpen()) closePreviewModal();
    });
    listenPreview(window, "keydown", ((event: KeyboardEvent) => {
        void handlePreviewKeydown(event);
    }) as EventListener, true);
    updateNavChrome();
    return teardownPreviewModal;
}
