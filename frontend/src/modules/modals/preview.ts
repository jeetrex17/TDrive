import { hasOperationErrorCode, isMobilePlatform, onRuntimeEvent, openExternalUrl } from '../../api';
import { state } from '../../state';
import { notify } from '../notifications';
import { enqueueDownload } from '../transfers';
import { renderImageInfoHTML } from './preview-info';
import { activateModalOwnership, deactivateModalOwnership, installModalA11y } from '../../ui/modals/modal-a11y';
import { pushSheet, type SheetHandle } from '../../ui/modals/sheet-stack';
import { bindTouchGestures, type TouchGestureHandlers } from '../../ui/preview/touch-gestures';
import { createZoomPanController } from '../../ui/preview/zoom-pan';
import { acquireRendition, subscribeRenditionReset, type ImageRequest } from '../renditions/runtime';
import type { RenditionLease } from '../renditions/broker';
import { getGalleryPolicy, subscribeGalleryPolicy } from '../gallery-policy';
import type { FileCommandItem } from '../../ui/file-list/types';
import {
    capturePreviewTransitionSource,
    createPreviewTransitionController,
    type PreviewTransitionSource,
} from './preview-transition';
import { previewElementsLive, resolvePreviewElements, type PreviewElements } from './preview-elements';
import { createUnlockCard, type UnlockCard } from './preview-unlock-card';
type PreviewCommandItem = Extract<FileCommandItem, { type: 'file' }>;
type PreviewSelection =
    | { reason: 'none' | 'multiple' | 'unsupported' }
    | { reason: 'ok'; item: PreviewCommandItem; key: string };

const SUPPORTED_EXTENSIONS = new Set(["jpg", "jpeg", "png", "gif", "webp", "bmp", "svg"]);

const PREVIEW_CHROME_HIDE_DELAY_MS = 1600;

// The whole element set, or nothing. Before this the controller held one
// variable per element and every function opened by re-checking whichever three
// or four it happened to write to, which said nothing about the rest: a
// half-built set was representable, so the guards had to keep pretending it was
// possible. Setup now either resolves every required element or refuses to run,
// so one check answers for all of them, and teardown is one assignment rather
// than twenty-five that had to be remembered.
let els: PreviewElements | null = null;
let unlockCard: UnlockCard | null = null;
let activeFullSrc = "";
let infoOpen = false;
// On a phone the info card is a sheet, so Android's BACK has to dismiss it
// before the preview under it.
let infoSheetBack: SheetHandle | null = null;

// Zoom and pan live in their own controller; the preview only decides when to
// reset them and whether a gesture belongs to them or to the swipe.
const zoomPan = createZoomPanController(() => els?.image ?? null);
// Touch double taps go through the phone recogniser; the dblclick the browser
// synthesises for them must not zoom a second time.
let lastPointerType = "mouse";

// Grid and viewer share one byte-budgeted broker. The displayed image stays
// pinned until navigation/close; at most one next preview has a separate lease.
let activeImageLease: RenditionLease | null = null;
let activeThumbnailLease: RenditionLease | null = null;
let prefetchedImageLease: RenditionLease | null = null;
let preloadEpoch = 0;
let unsubscribePreviewPolicy: (() => void) | null = null;
let unsubscribePreviewReset: (() => void) | null = null;
let previewRequestToken = 0;
let activePreviewKey = "";
let activePreviewMsgID = 0;
let activePreviewItem: any = null;
let chromeHideTimer: any = null;
let previewHostObserver: MutationObserver | null = null;
let previewHostEl: HTMLElement | null = null;
let previewA11y: ReturnType<typeof installModalA11y> | null = null;
let previewProgressUnsubscribe: (() => void) | null = null;
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
    activePreviewMsgID = 0;
    activePreviewItem = null;
    navSource = null;
    navigationPending = false;
    navigationEpoch += 1;
    updateNavChrome();
}

function isPreviewVisible() {
    const dom = els;
    return Boolean(dom && !dom.image.hidden && dom.image.getAttribute("src"));
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
    els?.modal.classList.toggle("is-chrome-visible", Boolean(visible));
}

function isErrorVisible() {
    const dom = els;
    return Boolean(dom && dom.error.style.display !== "none");
}

function isCloseButtonFocused() {
    return Boolean(els && els.closeButton === document.activeElement);
}

function scheduleChromeHide() {
    clearChromeHideTimer();

    if (!isPreviewOpen() || isErrorVisible()) return;
    if (isCloseButtonFocused()) return;

    chromeHideTimer = setTimeout(() => {
        if (!isPreviewOpen() || isErrorVisible()) return;
        if (isCloseButtonFocused()) return;
        setChromeVisible(false);
    }, PREVIEW_CHROME_HIDE_DELAY_MS);
}

function revealChrome() {
    if (!isPreviewOpen()) return;
    setChromeVisible(true);
    scheduleChromeHide();
}

function resetImageSurface() {
    const dom = els;
    if (!dom) return;
    dom.image.hidden = true;
    dom.image.removeAttribute("src");
    dom.image.alt = "";
}

function showPreviewLoading(_label?: any) {
    const dom = els;
    if (!dom) return;
    dom.modal.classList.add("is-preview-loading");
    dom.loading.style.display = "flex";
    dom.loading.setAttribute("aria-hidden", "false");
}

function setPreviewProgress(percent: any) {
    const dom = els;
    if (!dom) return;
    const clamped = Math.max(0, Math.min(100, Number(percent) || 0));
    showPreviewLoading();
    dom.loadingFill.style.width = `${clamped}%`;
}

function hidePreviewProgress() {
    const dom = els;
    if (!dom) return;
    dom.modal.classList.remove("is-preview-loading");
    dom.loading.style.display = "none";
    dom.loading.setAttribute("aria-hidden", "true");
    dom.loadingFill.style.width = "0%";
}

function preparePreviewSurface(filename: any, { keepCurrentImage = false } = {}) {
    const dom = els;
    if (!dom) return;
    hideLockedState();
    if (!keepCurrentImage) {
        dom.filename.textContent = filename || "Preview";
    }
    showPreviewLoading();
    dom.error.style.display = "none";
    dom.error.textContent = "";
    dom.modal.classList.remove("is-preview-error");

    if (!keepCurrentImage) resetImageSurface();
    if (!keepCurrentImage) dom.image.alt = "";
}

function showPreviewError(message: any, { keepCurrentImage = false } = {}) {
    const dom = els;
    if (!dom) return;

    previewTransition.cancel();
    hideLockedState();

    dom.modal.classList.remove("is-preview-locked");
    hidePreviewProgress();
    if (!keepCurrentImage) resetImageSurface();
    dom.error.textContent = message || "Download failed";
    dom.error.style.display = "block";
    dom.modal.classList.add("is-preview-error");
    setChromeVisible(true);
    clearChromeHideTimer();
}

function showPreviewImage(src: any, alt: any, { keepLoading = false } = {}) {
    const dom = els;
    if (!dom) return;

    hideLockedState();
    dom.modal.classList.remove("is-preview-locked");
    if (!keepLoading) {
        hidePreviewProgress();
    } else {
        showPreviewLoading(alt || "Preview");
    }
    dom.error.style.display = "none";
    dom.error.textContent = "";
    dom.modal.classList.remove("is-preview-error");
    dom.filename.textContent = alt || "Preview";
    dom.image.alt = "";
    dom.image.src = src;
    dom.image.hidden = false;
    // Opacity-only entrance: we drive transform via zoom/pan, so the animation
    // must not write transform (and must not hold it with fill).
    const sharedTransition = previewTransition.finishOpen(dom.image);
    const reduceMotion = typeof window.matchMedia === "function"
        && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    if (!sharedTransition && !previewTransition.isRunning() && !reduceMotion && typeof dom.image.animate === "function") {
        dom.image.animate(
            [{ opacity: 0.6 }, { opacity: 1 }],
            { duration: 180, easing: "cubic-bezier(0.22, 1, 0.36, 1)" },
        );
    }
    revealChrome();
}

function isPreviewOpen() {
    const dom = els;
    return Boolean(dom && dom.modal.style.display !== "none");
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

// The elements, or a complaint. "Setup completed" and "the elements are here"
// used to be two facts -- a `previewReady` flag and twenty-five variables -- and
// only the flag was ever consulted before writing to the elements.
function readyElements(): PreviewElements | null {
    if (els) return els;
    console.error("Preview modal is unavailable because setup did not complete.");
    flashStatus("Preview unavailable");
    return null;
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
        kind: 'preview',
    };
}

function releasePreviewImages(): void {
    preloadEpoch += 1;
    activeImageLease?.release();
    activeImageLease = null;
    activeThumbnailLease?.release();
    activeThumbnailLease = null;
    prefetchedImageLease?.release();
    prefetchedImageLease = null;
}

export async function loadPreview(target: PreviewNavigationItem) {
    const dom = readyElements();
    if (!dom) throw new Error('Preview unavailable');
    const token = ++previewRequestToken;
    const filename = target.name || 'Preview';
    activePreviewKey = getPreviewKey(target);
    activePreviewMsgID = Number(target.id);
    activePreviewItem = target;
    activeFullSrc = '';
    zoomPan.reset();
    resetImageSurface();
    updateNavChrome();
    refreshInfoPanel();
    setPreviewProgress(0);

    // Acquire before releasing the previous speculative lease. If this is the
    // prefetched neighbor, its transfer and cached bytes remain shared.
    const request = renditionRequest(target);
    const lease = acquireRendition(request, 'viewer');
    const placeholder = target.thumbUrl ? acquireRendition({ ...request, kind: 'thumbnail' }, 'viewer') : null;
    releasePreviewImages();
    activeImageLease = lease;
    activeThumbnailLease = placeholder;
    let previewSettled = false;
    // Pin the already-visible grid thumbnail while the screen preview arrives.
    // Reusing its lease avoids both a flash and a borrowed/revoked object URL.
    if (placeholder) void placeholder.promise.then(asset => {
        if (!previewSettled && token === previewRequestToken && isPreviewOpen()) showPreviewImage(asset.url, filename, { keepLoading: true });
    }).catch(() => {});

    try {
        const asset = await lease.promise;
        previewSettled = true;
        if (token !== previewRequestToken || !isPreviewOpen()) return null;
        showPreviewImage(asset.url, filename);
        activeThumbnailLease?.release();
        activeThumbnailLease = null;
        dom.image.title = 'Screen-sized preview. Download for original quality.';
        refreshInfoPanel();
        preloadNeighbors();
        return { src: asset.url };
    } catch (error) {
        previewSettled = true;
        if (token !== previewRequestToken || !isPreviewOpen()) return null;
        if (hasOperationErrorCode(error, 'encryption_password_required')
            || (error instanceof Error && 'code' in error && error.code === 'encryption_password_required')) {
            showLockedState();
            return null;
        }
        const normalized = normalizePreviewError(error);
        if (isPreviewVisible()) {
            hidePreviewProgress();
            dom.image.title = 'Thumbnail preview. Download for original quality.';
        } else showPreviewError(normalized.message);
        return null;
    }
}

export function closePreviewModal() {
    const dom = els;
    if (dom && zoomPan.scale === 1 && activePreviewTransitionSource && isPreviewVisible()) {
        previewTransition.playClose(activePreviewTransitionSource, dom.image);
    } else {
        previewTransition.cancel();
    }
    activePreviewTransitionSource = null;
    previewRequestToken += 1;
    releasePreviewImages();
    clearActivePreview();
    closeInfoPanel();
    hideLockedState();
    zoomPan.reset();
    activeFullSrc = "";
    clearChromeHideTimer();

    if (dom) {
        unlockCard?.clearInput();
        dom.modal.style.display = "none";
        dom.modal.setAttribute("aria-hidden", "true");
        previewA11y?.deactivate();
        deactivateModalOwnership(dom.modal);
        dom.modal.classList.remove("is-chrome-visible", "is-preview-error", "is-preview-locked", "is-shared-entering");
        dom.filename.textContent = "";
        dom.loading.style.display = "none";
        dom.error.style.display = "none";
        dom.error.textContent = "";
    }
    hidePreviewProgress();
    resetImageSurface();
}

// openPreviewItem shows the modal and loads one item. It does not touch the
// navigation context, so both single-item and list callers route through it.
async function openPreviewItem(item: any, transitionSource: PreviewTransitionSource | null = null) {
    const dom = els;
    if (!dom) return false;
    const wasOpen = isPreviewOpen();
    const keepCurrentImage = wasOpen && isPreviewVisible();

    previewTransition.cancel();
    activePreviewTransitionSource = transitionSource;
    dom.modal.style.display = "flex";
    dom.modal.setAttribute("aria-hidden", "false");
    previewA11y?.activate();
    activateModalOwnership(dom.modal);
    setChromeVisible(true);
    preparePreviewSurface(item.name || "Preview", { keepCurrentImage });
    if (!wasOpen && transitionSource && previewTransition.beginOpen(transitionSource, dom.modal)) {
        showPreviewImage(transitionSource.imageSrc, item.name || "Preview", { keepLoading: true });
    }

    try {
        await loadPreview(item);
        return true;
    } catch {
        return false;
    }
}

export async function openPreviewForSelection(target: PreviewCommandItem | null = null) {
    if (!readyElements()) return false;

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
    if (!readyElements()) return false;
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
    const dom = els;
    if (!dom) return;
    const position = navigationPosition();
    const hasList = Boolean(navSource && (!position || position.total > 1));
    if (dom.prevButton) {
        dom.prevButton.hidden = !hasList;
        dom.prevButton.disabled = !canNavigate(-1);
    }
    if (dom.nextButton) {
        dom.nextButton.hidden = !hasList;
        dom.nextButton.disabled = !canNavigate(1);
    }
    if (dom.counter) {
        dom.counter.hidden = !hasList || !position;
        dom.counter.textContent = hasList && position ? `${position.index + 1} / ${position.total}` : '';
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
    const dom = els;
    if (!dom?.infoPanel) return;
    infoOpen = true;
    // Clear whatever a drag left behind, so the sheet rises from the edge it
    // was thrown to rather than jumping there first.
    dom.infoPanel.style.transition = "";
    dom.infoPanel.style.transform = "";
    dom.modal.classList.add("is-info-open");
    dom.infoButton?.setAttribute("aria-pressed", "true");
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
    els?.modal.classList.remove("is-info-open");
    els?.infoButton?.setAttribute("aria-pressed", "false");
    infoSheetBack?.release();
    infoSheetBack = null;
}

// Derivative dimensions and stripped EXIF are not original metadata. Keep
// those fields unknown until a metadata source explicitly supplies them.
function refreshInfoPanel() {
    const dom = els;
    if (!infoOpen || !dom?.infoBody || !activePreviewItem) return;
    const hasFull = Boolean(activeFullSrc);
    dom.infoBody.innerHTML = renderImageInfoHTML({
        item: activePreviewItem,
        fullSrc: activeFullSrc,
        naturalWidth: hasFull ? dom.image.naturalWidth || 0 : 0,
        naturalHeight: hasFull ? dom.image.naturalHeight || 0 : 0,
    });
}

// --- encrypted "locked" state: an inline unlock card shown in place of the
// image, so navigating onto a locked photo never throws up a modal. ---

function showLockedState() {
    const dom = els;
    if (!dom?.locked || !unlockCard) return;
    previewTransition.cancel();
    hidePreviewProgress();
    resetImageSurface();
    dom.error.style.display = "none";
    dom.error.textContent = "";
    dom.modal.classList.remove("is-preview-error");
    unlockCard.show(String(state.encryption?.hint || "").trim());
    // Make the backdrop opaque so the gallery behind isn't visible through it.
    dom.modal.classList.add("is-preview-locked");
    setChromeVisible(true);
    clearChromeHideTimer();
}

function hideLockedState() {
    unlockCard?.hide();
}

// The reload has to name its photo now, not after the password round trip: by
// then the reader may have paged on, and reloading whatever is showing would
// drop them back where they were.
function submitInlineUnlock(): Promise<void> {
    const target = activePreviewItem;
    return unlockCard?.submit(() => {
        hideLockedState();
        if (target) void loadPreview(target);
    }) ?? Promise.resolve();
}

// --- zoom / pan ---

// Only ever zoom a picture the reader can actually see. The wheel and
// double-click paths check for themselves; a pinch arrives straight from the
// gesture recogniser, which knows nothing about whether an image loaded.
function zoomAt(clientX: number, clientY: number, factor: number) {
    if (!isPreviewVisible()) return;
    zoomPan.zoomAt(clientX, clientY, factor);
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
    if (zoomPan.scale > 1) zoomPan.reset();
    else zoomAt(e.clientX, e.clientY, 2.5);
}

// --- phone gestures: a tap toggles the chrome, a double tap and a pinch zoom,
// a sideways swipe moves through the set and a swipe down closes. ---

function settleDrag(animated: boolean) {
    const dom = els;
    if (!dom) return;
    const from = dom.image.style.transform;
    dom.image.style.transform = "";
    dom.modal.style.removeProperty("--preview-dismiss");
    if (!animated || !from || typeof dom.image.animate !== "function") return;
    const reduceMotion = typeof window.matchMedia === "function"
        && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    if (reduceMotion) return;
    dom.image.animate([{ transform: from }, { transform: "none" }], { duration: 180, easing: "cubic-bezier(0.2, 0, 0, 1)" });
}

// The info card is a sheet on a phone, so it goes the way a sheet goes: a tap
// on the picture behind it, a drag down, or BACK. It is a sibling of the stage,
// so none of this touches the stage's own swipe.
const INFO_DISMISS_PX = 96;
const INFO_DISMISS_VELOCITY = 0.6;

function settleInfoSheet(to: string) {
    const panel = els?.infoPanel;
    if (!panel) return;
    panel.style.transition = "";
    panel.style.transform = to;
}

function infoTouchHandlers(): TouchGestureHandlers {
    return {
        dragStart: (axis) => axis === "y" && infoOpen && (els?.infoBody?.scrollTop || 0) <= 0,
        drag: (_dx, dy) => {
            const panel = els?.infoPanel;
            if (!panel || (els?.infoBody?.scrollTop || 0) > 0) return;
            panel.style.transition = "none";
            panel.style.transform = `translate3d(0, ${Math.max(0, dy)}px, 0)`;
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
            const dom = els;
            if (!isPreviewOpen() || !dom) return;
            if (infoOpen) {
                closeInfoPanel();
                return;
            }
            clearChromeHideTimer();
            setChromeVisible(!dom.modal.classList.contains("is-chrome-visible"));
        },
        doubleTap: (x, y) => {
            if (!isPreviewVisible()) return;
            if (zoomPan.scale > 1) zoomPan.reset();
            else zoomAt(x, y, 2.5);
        },
        pinchStart: () => zoomPan.endPan(),
        pinch: (factor, x, y) => zoomAt(x, y, factor),
        // A zoomed picture pans instead; the pan handlers above own that pointer.
        dragStart: () => zoomPan.scale === 1 && isPreviewVisible() && !els?.modal.classList.contains("is-preview-locked"),
        drag: (dx, dy, axis) => {
            const dom = els;
            if (!dom) return;
            if (axis === "x") {
                dom.image.style.transform = `translate3d(${dx}px, 0, 0)`;
                return;
            }
            const drop = Math.max(0, dy);
            dom.image.style.transform = `translate3d(0, ${drop}px, 0) scale(${Math.max(0.82, 1 - drop / 1400)})`;
            dom.modal.style.setProperty("--preview-dismiss", String(Math.min(1, drop / 240)));
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

// Speculation fetches compressed screen previews only. No Image/decode call
// here: invisible neighbors must not allocate decoded WebView surfaces.
function preloadNeighbors() {
    if (!navSource || !activePreviewItem || !getGalleryPolicy().allowPrefetch) return;
    const epoch = ++preloadEpoch;
    const source = navSource;
    const item = activePreviewItem;
    void source.getNeighbor(item, 1).then(next => {
        if (!next || epoch !== preloadEpoch || !isPreviewOpen() || !getGalleryPolicy().allowPrefetch) return;
        prefetchedImageLease?.release();
        prefetchedImageLease = acquireRendition(renditionRequest(next), 'prefetch');
        void prefetchedImageLease.promise.catch(() => {});
    }).catch(() => {}); // Navigation reports an unavailable neighbor on demand.
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
    if (els) deactivateModalOwnership(els.modal);
    previewA11y = null;
    previewProgressUnsubscribe?.();
    previewProgressUnsubscribe = null;
    for (let i = previewListenerCleanups.length - 1; i >= 0; i -= 1) {
        previewListenerCleanups[i]();
    }
    previewListenerCleanups.length = 0;
    previewHostObserver?.disconnect();
    previewHostObserver = null;
    previewHostEl = null;
    previewTransition.cancel();
    releasePreviewImages();
    unsubscribePreviewPolicy?.();
    unsubscribePreviewPolicy = null;
    unsubscribePreviewReset?.();
    unsubscribePreviewReset = null;
    clearActivePreview();
    infoOpen = false;
    infoSheetBack?.release();
    infoSheetBack = null;
    els = null;
    unlockCard = null;
}

export function activatePreviewModal(): () => void {
    const host = document.getElementById("preview-modal");
    if (!host) {
        if (previewHostEl) teardownPreviewModal();
        return () => {};
    }

    // Reuse only if the elements we already hold are the ones on screen. Asking
    // them, rather than asking the document whether something with each id
    // exists, is what stops a re-rendered shell from leaving every write landing
    // on detached nodes while the modal merely looks frozen.
    if (previewHostEl === host && els && previewElementsLive(els)) {
        updateNavChrome();
        return teardownPreviewModal;
    }
    if (previewHostEl || els) teardownPreviewModal();

    previewHostEl = host;
    previewHostObserver = new MutationObserver(() => {
        if (!host.isConnected) teardownPreviewModal();
    });
    previewHostObserver.observe(document.body, { childList: true, subtree: true });

    const resolved = resolvePreviewElements(host);
    if ('missing' in resolved) {
        console.error("Preview modal setup failed. Missing DOM elements: " + resolved.missing.join(", "));
        teardownPreviewModal();
        return () => {};
    }

    const dom = resolved.elements;
    els = dom;
    unlockCard = createUnlockCard({
        card: dom.locked,
        input: dom.lockedInput,
        unlockButton: dom.lockedUnlockButton,
        eyeButton: dom.lockedEyeButton,
        error: dom.lockedError,
        hint: dom.lockedHint,
        hintText: dom.lockedHintText,
    });
    previewA11y = installModalA11y(dom.modal, {
        requestClose: closePreviewModal,
        initialFocus: () => dom.closeButton,
        restoreFocus: () => state.virtualView === "photos"
            ? document.getElementById("gallery-view")
            : document.getElementById("file-list"),
    });

    listenPreview(dom.prevButton, "click", ((event: MouseEvent) => {
        event.stopPropagation();
        void navigatePreview(-1);
    }) as EventListener);
    listenPreview(dom.nextButton, "click", ((event: MouseEvent) => {
        event.stopPropagation();
        void navigatePreview(1);
    }) as EventListener);
    listenPreview(dom.downloadButton, "click", ((event: MouseEvent) => {
        event.stopPropagation();
        handleDownloadFromPreview();
    }) as EventListener);
    listenPreview(dom.infoButton, "click", ((event: MouseEvent) => {
        event.stopPropagation();
        toggleInfoPanel();
    }) as EventListener);
    listenPreview(dom.infoCloseButton, "click", ((event: MouseEvent) => {
        event.stopPropagation();
        closeInfoPanel();
    }) as EventListener);
    listenPreview(dom.lockedUnlockButton, "click", ((event: MouseEvent) => {
        event.stopPropagation();
        void submitInlineUnlock();
    }) as EventListener);
    listenPreview(dom.lockedEyeButton, "click", ((event: MouseEvent) => {
        event.stopPropagation();
        unlockCard?.toggleReveal();
    }) as EventListener);
    listenPreview(dom.lockedInput, "keydown", ((event: KeyboardEvent) => {
        if (event.key === "Enter") {
            event.preventDefault();
            void submitInlineUnlock();
        }
        event.stopPropagation();
    }) as EventListener);
    listenPreview(dom.infoPanel, "click", ((event: MouseEvent) => {
        const link = (event.target as HTMLElement).closest("[data-map-url]") as HTMLElement | null;
        if (!link) return;
        event.stopPropagation();
        const url = link.getAttribute("data-map-url") || "";
        if (url) openExternalUrl(url);
    }) as EventListener);
    listenPreview(dom.closeButton, "click", closePreviewModal as EventListener);
    listenPreview(dom.modal, "click", ((event: MouseEvent) => {
        // A pan ends in a click on the picture, and that click must not be
        // read as a click on the backdrop and close the photo being panned.
        if (zoomPan.consumePanMoved()) return;
        // On a phone a tap on the picture toggles the chrome; the X and a swipe
        // down close.
        if (isMobilePlatform()) return;
        if (event.target === dom.modal || event.target === dom.shell || event.target === dom.stage) closePreviewModal();
    }) as EventListener);
    listenPreview(dom.modal, "pointermove", ((event: PointerEvent) => {
        if (event.pointerType === "touch" && isMobilePlatform()) return;
        if (isPreviewOpen()) revealChrome();
    }) as EventListener);
    if (isMobilePlatform()) {
        listenPreview(dom.stage, "pointerdown", ((event: PointerEvent) => {
            lastPointerType = event.pointerType;
        }) as EventListener, true);
        previewListenerCleanups.push(bindTouchGestures(dom.stage, previewTouchHandlers()));
        // The info sheet is optional markup, and binding gestures to nothing
        // threw here rather than simply leaving the sheet undraggable.
        if (dom.infoPanel) {
            previewListenerCleanups.push(bindTouchGestures(dom.infoPanel, infoTouchHandlers()));
        }
    }
    listenPreview(dom.stage, "wheel", handleZoomWheel as EventListener, { passive: false });
    listenPreview(dom.image, "dblclick", handleZoomDblClick as EventListener);
    listenPreview(dom.image, "pointerdown", ((event: PointerEvent) => zoomPan.pointerDown(event)) as EventListener);
    listenPreview(dom.image, "pointermove", ((event: PointerEvent) => zoomPan.pointerMove(event)) as EventListener);
    listenPreview(dom.image, "pointerup", ((event: PointerEvent) => zoomPan.pointerUp(event)) as EventListener);
    listenPreview(dom.image, "pointercancel", ((event: PointerEvent) => zoomPan.pointerUp(event)) as EventListener);
    listenPreview(dom.closeButton, "focus", (() => {
        setChromeVisible(true);
        clearChromeHideTimer();
    }) as EventListener);
    listenPreview(dom.closeButton, "blur", (() => scheduleChromeHide()) as EventListener);
    listenPreview(dom.image, "error", (() => {
        if (isPreviewOpen() && dom.image?.getAttribute("src")) showPreviewError("Not a supported image");
    }) as EventListener);
    listenPreview(dom.image, "load", (() => {
        if (infoOpen) refreshInfoPanel();
    }) as EventListener);
    unsubscribePreviewReset = subscribeRenditionReset(() => {
        if (isPreviewOpen()) closePreviewModal();
    });
    unsubscribePreviewPolicy = subscribeGalleryPolicy(policy => {
        if (!policy.allowPrefetch) {
            preloadEpoch += 1;
            prefetchedImageLease?.release();
            prefetchedImageLease = null;
        }
        if (policy.backgrounded && isPreviewOpen()) closePreviewModal();
    });
    previewProgressUnsubscribe = onRuntimeEvent("preview_progress", (msgID, percent) => {
        if (!isPreviewOpen()) return;
        const targetID = Number(msgID);
        if (!Number.isFinite(targetID) || targetID !== activePreviewMsgID) return;
        setPreviewProgress(percent);
    });
    listenPreview(window, "keydown", ((event: KeyboardEvent) => {
        void handlePreviewKeydown(event);
    }) as EventListener, true);
    updateNavChrome();
    return teardownPreviewModal;
}
