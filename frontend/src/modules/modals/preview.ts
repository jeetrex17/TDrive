import { getPreviewFile, getPreviewThumbnail, hasOperationErrorCode, isMobilePlatform, onRuntimeEvent, openExternalUrl, useEncryptionPassword } from '../../api';
import { state } from '../../state';
import { notify } from '../notifications';
import { loadEncryptionStatus } from '../encryption';
import { enqueueDownload } from '../transfers';
import { renderImageInfoHTML } from './preview-info';
import { activateModalOwnership, deactivateModalOwnership, installModalA11y } from '../../ui/modals/modal-a11y';
import { pushSheet, type SheetHandle } from '../../ui/modals/sheet-stack';
import { bindTouchGestures, type TouchGestureHandlers } from '../../ui/preview/touch-gestures';
import type { PreviewPayload } from '../../types';
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

const SUPPORTED_EXTENSIONS = new Set(["jpg", "jpeg", "png", "gif", "webp", "bmp", "svg"]);

const PREVIEW_CHROME_HIDE_DELAY_MS = 1600;
const REQUIRED_ELEMENT_IDS = [
    "preview-modal",
    "preview-shell",
    "preview-stage",
    "preview-filename",
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

// Full-resolution data URLs keyed by drive + msgID, with neighbor prefetch so
// next/prev is instant. Telegram message ids are scoped to a channel, so using
// msgID alone can show the wrong image after switching drives.
const FULL_CACHE_MAX = 12;
const fullCache = new Map<string, string>();
// In-flight full-image downloads keyed by drive + msgID, so a neighbor prefetch
// and the user's own navigation to the same image share one download instead of
// racing two (which would serialize on the backend's preview mutex).
const inflightFull = new Map<string, Promise<string>>();
let preloadEpoch = 0;
let previewReady = false;
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

// Lightbox navigation context. When opened from the gallery this holds the
// ordered image set and the current position so ←/→ and the on-screen chevrons
// can page through it. A single-item open (file-list preview) leaves it empty.
let navItems: any[] = [];
let navIndex = -1;

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
    navItems = [];
    navIndex = -1;
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

function resetImageSurface() {
    if (!imageEl) return;
    imageEl.hidden = true;
    imageEl.removeAttribute("src");
    imageEl.alt = "";
}

function showPreviewLoading(_label?: any) {
    if (!modalEl || !loadingEl) return;
    modalEl.classList.add("is-preview-loading");
    loadingEl.style.display = "flex";
    loadingEl.setAttribute("aria-hidden", "false");
}

function setPreviewProgress(percent: any) {
    if (!loadingEl || !loadingFillEl) return;
    const clamped = Math.max(0, Math.min(100, Number(percent) || 0));
    showPreviewLoading();
    loadingFillEl.style.width = `${clamped}%`;
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
    imageEl.alt = "";
    imageEl.src = src;
    imageEl.hidden = false;
    // Opacity-only entrance: we drive transform via zoom/pan, so the animation
    // must not write transform (and must not hold it with fill).
    const sharedTransition = previewTransition.finishOpen(imageEl);
    const reduceMotion = typeof window.matchMedia === "function"
        && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    if (!sharedTransition && !previewTransition.isRunning() && !reduceMotion && typeof imageEl.animate === "function") {
        imageEl.animate(
            [{ opacity: 0.6 }, { opacity: 1 }],
            { duration: 180, easing: "cubic-bezier(0.22, 1, 0.36, 1)" },
        );
    }
    revealChrome();
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

function buildPreviewSource(mimeType: string, dataBase64: string) {
    return 'data:' + mimeType + ';base64,' + dataBase64;
}

function payloadToPreviewAsset(payload: PreviewPayload) {
    const dataBase64 = payload.dataBase64;
    const mimeType = payload.mimeType;
    if (!dataBase64 || !mimeType) {
        throw new Error("Download failed");
    }

    return {
        src: buildPreviewSource(mimeType, dataBase64),
        mimeType,
    };
}

async function decodePreviewSource(src: any) {
    const preloaded = new Image();
    preloaded.decoding = "async";
    preloaded.src = src;

    if (typeof preloaded.decode === "function") {
        try {
            await preloaded.decode();
            return;
        } catch (err) {
            if (preloaded.complete && preloaded.naturalWidth > 0) return;
            throw err;
        }
    }

    if (preloaded.complete && preloaded.naturalWidth > 0) return;

    await new Promise((resolve, reject) => {
        preloaded.addEventListener("load", resolve, { once: true });
        preloaded.addEventListener("error", () => reject(new Error("Not a supported image")), { once: true });
    });
}

async function resolveThumbnailPreviewEntry(target: any) {
    const msgID = Number(target?.id || 0);
    if (!msgID) {
        throw new Error("Download failed");
    }

    const asset = payloadToPreviewAsset(await getPreviewThumbnail(msgID));
    await decodePreviewSource(asset.src);
    return asset;
}

async function resolveFullPreviewEntry(target: any) {
    const msgID = Number(target?.id || 0);
    if (!msgID) {
        throw new Error("Download failed");
    }

    // Shares an in-flight neighbor prefetch for the same image. A locked
    // encrypted file returns the stable encryption_password_required code; loadPreview
    // turns that into the inline unlock card rather than a popup modal.
	return { src: await fetchFullRaw(target), mimeType: "" };
}

export async function loadPreview(target: any) {
    if (!assertPreviewReady()) {
        throw new Error("Preview unavailable");
    }
    const msgID = Number(target?.id || 0);
    const previewKey = getPreviewKey(target);
    const filename = String(target?.name || filenameEl?.textContent || "Preview");
    const token = ++previewRequestToken;
    // Stop the previous image's neighbor prefetch so this load doesn't queue
    // behind its remaining downloads (the in-flight one is shared via fetchFullRaw).
    preloadEpoch += 1;
    // Navigation commits to the target: activePreview* always reflect the item
    // the user is on, so the counter, info panel, and download stay in agreement
    // even when the full-size load fails.
    activePreviewKey = previewKey;
    activePreviewMsgID = msgID;
    activePreviewItem = target;
    activeFullSrc = "";
    resetZoom();
    refreshInfoPanel();

    if (!msgID || !previewKey) {
        const err = new Error("Download failed");
        if (token === previewRequestToken && isPreviewOpen()) {
            showPreviewError(err.message);
        }
        throw err;
    }

    // Whether a usable image (placeholder or full) is currently standing in for
    // this request, and whether the full-size load has settled.
    let placeholderShown = false;
    let fullSettled = false;
    // Resolves to a fallback thumbnail src ("" if none) for when the full-size
    // load fails (e.g. over the preview budget) so we show an image, not an error.
    let thumbPromise: Promise<string> = Promise.resolve("");

    try {
        // Already prefetched by a neighbor preload? Show it instantly with no
        // loading indicator at all.
		const cachedFull = fullCache.get(previewKey);
        if (cachedFull) {
            activeFullSrc = cachedFull;
            showPreviewImage(cachedFull, filename);
            refreshInfoPanel();
            preloadNeighbors();
            return { src: cachedFull };
        }

        setPreviewProgress(0);

        // Instant low-res placeholder. The gallery hands us a thumbnail data
        // URL it already loaded (zero extra work); elsewhere we fall back to a
        // server-side thumbnail fetch. Either becomes the standing image until
        // the full-size load lands.
        const initialThumb = String(target?.thumbUrl || "");
        if (initialThumb) {
            if (token === previewRequestToken && isPreviewOpen()) {
                showPreviewImage(initialThumb, filename, { keepLoading: true });
                placeholderShown = true;
            }
        } else {
            thumbPromise = resolveThumbnailPreviewEntry(target)
                .then((asset) => String(asset?.src || ""))
                .catch(() => "");
            void thumbPromise.then((src) => {
                // Show as a placeholder only while the full load is still pending;
                // once it settles, the catch/ success path owns what's displayed.
                if (!src || fullSettled || token !== previewRequestToken || !isPreviewOpen()) return;
                showPreviewImage(src, filename, { keepLoading: true });
                placeholderShown = true;
            });
        }

        const asset = await resolveFullPreviewEntry(target);
        fullSettled = true;
        if (token !== previewRequestToken || !isPreviewOpen()) return null;

        if (!asset?.src) {
            throw new Error("Download failed");
        }

        activeFullSrc = asset.src;
        showPreviewImage(asset.src, filename);
        refreshInfoPanel();
        preloadNeighbors();
        return asset;
    } catch (err) {
        fullSettled = true;
        if (token !== previewRequestToken || !isPreviewOpen()) return null;

        // Locked encrypted photo: show the inline unlock card in place of the
        // image, never a popup modal, so navigation stays uninterrupted.
        if (hasOperationErrorCode(err, 'encryption_password_required')) {
            showLockedState();
            return null;
        }

        // If a placeholder image is standing in, keep it: an image over the
        // full-size budget, or a cancelled unlock, should still show the
        // thumbnail rather than a hard error. Only error when we have nothing.
        if (placeholderShown && isPreviewVisible()) {
            hidePreviewProgress();
            return null;
        }
        // Nothing shown yet: if a thumbnail is still on its way, show it instead
        // of a hard error (e.g. an image over the full-size preview budget).
        const thumbSrc = await thumbPromise;
        if (token !== previewRequestToken || !isPreviewOpen()) return null;
        if (thumbSrc) {
            showPreviewImage(thumbSrc, filename);
            return null;
        }
        const normalized = normalizePreviewError(err);
        showPreviewError(normalized.message);
        throw normalized;
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
    preloadEpoch += 1; // abort any in-flight neighbor prefetch
    // Drop the full-image cache between sessions: it's keyed by msg id, which is
    // only unique within a drive, so a stale entry must not survive a drive switch.
    fullCache.clear();
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
    setChromeVisible(true);
    preparePreviewSurface(item.name || "Preview", { keepCurrentImage });
    if (!wasOpen && transitionSource && previewTransition.beginOpen(transitionSource, modalEl)) {
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
    navItems = [];
    navIndex = -1;
    updateNavChrome();
    return openPreviewItem(selection.item);
}

function findGalleryPreviewSource(item: any): PreviewTransitionSource | null {
    const id = Number(item?.id || 0);
    if (!id) return null;
    const cell = document.querySelector<HTMLElement>('.gallery-cell[data-id="' + id + '"]');
    return capturePreviewTransitionSource(cell);
}

// openPreviewList opens the lightbox on items[index] with ←/→ navigation across
// the whole list. Items are { type:"file", id, name, size?, thumbUrl? }.
export async function openPreviewList(
    items: any[],
    index: number,
    transitionSource: PreviewTransitionSource | null = null,
) {
    if (!assertPreviewReady()) return false;
    if (!Array.isArray(items) || items.length === 0) return false;

    const i = Math.max(0, Math.min(items.length - 1, Number(index) || 0));
    navItems = items;
    navIndex = i;
    updateNavChrome();
    return openPreviewItem(items[i], transitionSource || findGalleryPreviewSource(items[i]));
}

async function navigatePreview(delta: number) {
    if (!isPreviewOpen() || navItems.length === 0) return;
    const next = navIndex + delta;
    if (next < 0 || next >= navItems.length) return;
    navIndex = next;
    updateNavChrome();
    await openPreviewItem(navItems[next], findGalleryPreviewSource(navItems[next]));
}

function updateNavChrome() {
    const hasList = navItems.length > 1;
    if (prevBtnEl) {
        prevBtnEl.hidden = !hasList;
        prevBtnEl.disabled = navIndex <= 0;
    }
    if (nextBtnEl) {
        nextBtnEl.hidden = !hasList;
        nextBtnEl.disabled = navIndex >= navItems.length - 1;
    }
    if (counterEl) {
        counterEl.hidden = !hasList;
        counterEl.textContent = hasList ? `${navIndex + 1} / ${navItems.length}` : "";
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

// refreshInfoPanel re-renders the panel for the active item. Dimensions are
// only sourced from the displayed <img> once the full image is in (activeFullSrc
// set); until then we rely on EXIF, so a thumbnail's size never leaks in.
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
        await useEncryptionPassword(value);
        await loadEncryptionStatus();
        if (lockedInputEl) lockedInputEl.value = "";
        // Let the gallery's locked thumbnail cells reload too.
        window.dispatchEvent(new Event("tdrive:unlocked"));
        hideLockedState();
        if (target) void loadPreview(target); // re-load the photo, now decryptable
    } catch (err) {
        showLockedError(String(err) || "Incorrect password");
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
                const next = navIndex + step;
                if ((Math.abs(dx) > 56 || velocity > 0.5) && next >= 0 && next < navItems.length) {
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

// --- full-image cache + neighbor prefetch ---

function cacheFull(key: string, src: string) {
	fullCache.set(key, src);
	if (fullCache.size > FULL_CACHE_MAX) {
		const oldest = fullCache.keys().next().value;
		if (oldest !== undefined) fullCache.delete(oldest);
	}
}

// fetchFullRaw downloads + decodes + caches one full image, returning its data
// URL. Concurrent callers for the same id share a single download. It never
// opens the unlock modal; callers that need it wrap this and retry.
function fetchFullRaw(item: any): Promise<string> {
	const id = Number(item?.id || 0);
	const key = getPreviewKey(item);
	const cached = fullCache.get(key);
	if (cached) return Promise.resolve(cached);
	const existing = inflightFull.get(key);
	if (existing) return existing;

	const p = (async () => {
		const asset = payloadToPreviewAsset(await getPreviewFile(id));
		await decodePreviewSource(asset.src);
		cacheFull(key, asset.src);
		return asset.src;
	})();
	inflightFull.set(key, p);
	void p.catch(() => {}).finally(() => {
		if (inflightFull.get(key) === p) inflightFull.delete(key);
	});
	return p;
}

// preloadNeighbors prefetches the next/prev few full images so navigation is
// instant. It runs sequentially and aborts the instant the user navigates
// again (preloadEpoch), so it never queues many downloads ahead of an
// on-demand load. PreviewFile is called raw here so a locked image is skipped
// rather than popping the password modal during a background prefetch.
function preloadNeighbors() {
    if (navItems.length <= 1) return;
    const epoch = ++preloadEpoch;
    const baseIndex = navIndex;
    void (async () => {
        for (const off of [1, -1, 2, -2, 3, -3]) {
            if (epoch !== preloadEpoch) return;
            const idx = baseIndex + off;
            if (idx < 0 || idx >= navItems.length) continue;
			const item = navItems[idx];
			const id = Number(item?.id || 0);
			const key = getPreviewKey(item);
			if (!id || !key || fullCache.has(key)) continue;
			try {
				await fetchFullRaw(item);
			} catch {
                // Too large, locked, or failed — the on-demand view handles it.
            }
            if (epoch !== preloadEpoch) return;
        }
    })();
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
        if (navItems.length <= 1) return;
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
    previewProgressUnsubscribe?.();
    previewProgressUnsubscribe = null;
    for (let i = previewListenerCleanups.length - 1; i >= 0; i -= 1) {
        previewListenerCleanups[i]();
    }
    previewListenerCleanups.length = 0;
    previewHostObserver?.disconnect();
    previewHostObserver = null;
    previewHostEl = null;
    previewReady = false;
    previewTransition.cancel();
    preloadEpoch += 1;
    fullCache.clear();
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
        if (isPreviewOpen() && imageEl?.getAttribute("src")) showPreviewError("Not a supported image");
    }) as EventListener);
    listenPreview(imageEl, "load", (() => {
        if (infoOpen) refreshInfoPanel();
    }) as EventListener);
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
