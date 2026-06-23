import { PreviewFile, PreviewThumbnail, UseEncryptionPassword } from '../../../wailsjs/go/main/App';
import { state } from '../../state';
import { notify } from '../notifications';
import { loadEncryptionStatus } from '../encryption';
import { renderImageInfoHTML } from './preview-info';

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

// Lightbox navigation context. When opened from the gallery this holds the
// ordered image set and the current position so ←/→ and the on-screen chevrons
// can page through it. A single-item open (file-list preview) leaves it empty.
let navItems: any[] = [];
let navIndex = -1;

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

    const contextMenu = document.getElementById("context-menu");
    return Boolean(contextMenu && contextMenu.style.display !== "none");
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

function getSelectedPreviewTarget() {
    const items = Array.from(state.selectedItems.values());
    if (items.length === 0) return { reason: "none" };
    if (items.length > 1) return { reason: "multiple" };

    const item = items[0];
    if (!item || item.type !== "file") return { reason: "unsupported" };
    if (!isPreviewableImage(item.name)) return { reason: "unsupported" };

    return { reason: "ok", item, key: getPreviewKey(item) };
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
    if (typeof imageEl.animate === "function") {
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

function buildPreviewSource(mimeType: any, dataBase64: any) {
    return `data:${mimeType};base64,${dataBase64}`;
}

function payloadToPreviewAsset(payload: any) {
    const dataBase64 = String(payload?.data_base64 || "");
    const mimeType = String(payload?.mime_type || "");
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

    const asset = payloadToPreviewAsset(await PreviewThumbnail(msgID));
    await decodePreviewSource(asset.src);
    return asset;
}

async function resolveFullPreviewEntry(target: any) {
    const msgID = Number(target?.id || 0);
    if (!msgID) {
        throw new Error("Download failed");
    }

    // Shares an in-flight neighbor prefetch for the same image. A locked
    // encrypted file rejects with "encryption password required"; loadPreview
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
        if (/encryption password required/i.test(String(err))) {
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
        modalEl.classList.remove("is-chrome-visible", "is-preview-error", "is-preview-locked");
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
async function openPreviewItem(item: any) {
    const keepCurrentImage = isPreviewOpen() && isPreviewVisible();

    modalEl.style.display = "flex";
    modalEl.setAttribute("aria-hidden", "false");
    setChromeVisible(true);
    preparePreviewSurface(item.name || "Preview", { keepCurrentImage });

    try {
        await loadPreview(item);
        return true;
    } catch {
        return false;
    }
}

export async function openPreviewForSelection(target = null) {
    if (!assertPreviewReady()) return false;

    const selection = target
        ? { reason: "ok", item: target, key: getPreviewKey(target) }
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

// openPreviewList opens the lightbox on items[index] with ←/→ navigation across
// the whole list. Items are { type:"file", id, name, size?, thumbUrl? }.
export async function openPreviewList(items: any[], index: number) {
    if (!assertPreviewReady()) return false;
    if (!Array.isArray(items) || items.length === 0) return false;

    const i = Math.max(0, Math.min(items.length - 1, Number(index) || 0));
    navItems = items;
    navIndex = i;
    updateNavChrome();
    return openPreviewItem(items[i]);
}

async function navigatePreview(delta: number) {
    if (!isPreviewOpen() || navItems.length === 0) return;
    const next = navIndex + delta;
    if (next < 0 || next >= navItems.length) return;
    navIndex = next;
    updateNavChrome();
    await openPreviewItem(navItems[next]);
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
    if (typeof window.initDownload === "function") {
        window.initDownload(id, name, size);
    }
}

function toggleInfoPanel() {
    if (infoOpen) closeInfoPanel();
    else openInfoPanel();
}

function openInfoPanel() {
    if (!infoPanelEl || !modalEl) return;
    infoOpen = true;
    modalEl.classList.add("is-info-open");
    infoBtnEl?.setAttribute("aria-pressed", "true");
    refreshInfoPanel();
}

function closeInfoPanel() {
    infoOpen = false;
    modalEl?.classList.remove("is-info-open");
    infoBtnEl?.setAttribute("aria-pressed", "false");
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

const EYE_OPEN_SVG = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path stroke-linecap="round" stroke-linejoin="round" d="M2.5 12S6 5.5 12 5.5s9.5 6.5 9.5 6.5-3.5 6.5-9.5 6.5S2.5 12 2.5 12Z"/><circle cx="12" cy="12" r="3.2"/></svg>';
const EYE_OFF_SVG = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path stroke-linecap="round" stroke-linejoin="round" d="M9.9 5.7A9.6 9.6 0 0 1 12 5.5c6 0 9.5 6.5 9.5 6.5a16.3 16.3 0 0 1-2.9 3.6M6.2 7.2A15.9 15.9 0 0 0 2.5 12S6 18.5 12 18.5c1.4 0 2.6-.2 3.7-.7M3 3l18 18M9.9 9.9a3 3 0 0 0 4.2 4.2"/></svg>';

// Show/hide the typed password. A ghost toggle inside the pill (a polish
// hallmark for password fields); resets to hidden whenever the state reopens.
function toggleLockedReveal() {
    if (!lockedInputEl || !lockedEyeEl) return;
    const reveal = lockedInputEl.type === "password";
    lockedInputEl.type = reveal ? "text" : "password";
    lockedEyeEl.innerHTML = reveal ? EYE_OFF_SVG : EYE_OPEN_SVG;
    lockedEyeEl.setAttribute("aria-pressed", reveal ? "true" : "false");
    lockedEyeEl.setAttribute("aria-label", reveal ? "Hide password" : "Show password");
    try { lockedInputEl.focus(); } catch { /* focus is best-effort */ }
}

function resetLockedReveal() {
    if (lockedInputEl) lockedInputEl.type = "password";
    if (lockedEyeEl) {
        lockedEyeEl.innerHTML = EYE_OPEN_SVG;
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
        await UseEncryptionPassword(value);
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
		const asset = payloadToPreviewAsset(await PreviewFile(id));
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
        if (selection.reason === "none") return;
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

export function setupPreviewModal() {
    const missing = REQUIRED_ELEMENT_IDS.filter((id) => !document.getElementById(id));
    if (missing.length) {
        previewReady = false;
        console.error(`Preview modal setup failed. Missing DOM elements: ${missing.join(", ")}`);
        return false;
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
    // Optional chrome: list navigation + download. Absent in older markup, so
    // these are not in REQUIRED_ELEMENT_IDS and every use is guarded.
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

    if (prevBtnEl) {
        prevBtnEl.addEventListener("click", (e: any) => {
            e.stopPropagation();
            void navigatePreview(-1);
        });
    }
    if (nextBtnEl) {
        nextBtnEl.addEventListener("click", (e: any) => {
            e.stopPropagation();
            void navigatePreview(1);
        });
    }
    if (downloadBtnEl) {
        downloadBtnEl.addEventListener("click", (e: any) => {
            e.stopPropagation();
            handleDownloadFromPreview();
        });
    }
    if (infoBtnEl) {
        infoBtnEl.addEventListener("click", (e: any) => {
            e.stopPropagation();
            toggleInfoPanel();
        });
    }
    if (infoCloseBtnEl) {
        infoCloseBtnEl.addEventListener("click", (e: any) => {
            e.stopPropagation();
            closeInfoPanel();
        });
    }
    if (lockedUnlockEl) {
        lockedUnlockEl.addEventListener("click", (e: any) => {
            e.stopPropagation();
            void submitInlineUnlock();
        });
    }
    if (lockedEyeEl) {
        lockedEyeEl.addEventListener("click", (e: any) => {
            e.stopPropagation();
            toggleLockedReveal();
        });
    }
    if (lockedInputEl) {
        lockedInputEl.addEventListener("keydown", (e: any) => {
            if (e.key === "Enter") {
                e.preventDefault();
                void submitInlineUnlock();
            }
            // Don't let typing (Space/arrows/i) trigger preview keyboard shortcuts.
            e.stopPropagation();
        });
    }
    if (infoPanelEl) {
        // Map links open in the system browser. The panel is rebuilt via
        // innerHTML, so handle clicks by delegation.
        infoPanelEl.addEventListener("click", (e: any) => {
            const link = (e.target as HTMLElement).closest("[data-map-url]") as HTMLElement | null;
            if (!link) return;
            e.stopPropagation();
            const url = link.getAttribute("data-map-url") || "";
            if (!url) return;
            if (window.runtime?.BrowserOpenURL) window.runtime.BrowserOpenURL(url);
            else window.open(url, "_blank");
        });
    }
    updateNavChrome();

    closeBtnEl.addEventListener("click", closePreviewModal);
    modalEl.addEventListener("click", (event: any) => {
        // A pan drag can end with a click on the backdrop; don't treat it as
        // a close.
        if (panMoved) {
            panMoved = false;
            return;
        }
        if (event.target === modalEl || event.target === shellEl || event.target === stageEl) {
            closePreviewModal();
        }
    });
    modalEl.addEventListener("pointermove", () => {
        if (!isPreviewOpen()) return;
        revealChrome();
    });

    // Zoom + pan on the image. Wheel zooms toward the cursor, double-click
    // toggles, and dragging pans while zoomed.
    stageEl.addEventListener("wheel", handleZoomWheel, { passive: false });
    imageEl.addEventListener("dblclick", handleZoomDblClick);
    imageEl.addEventListener("pointerdown", handlePanStart);
    imageEl.addEventListener("pointermove", handlePanMove);
    imageEl.addEventListener("pointerup", handlePanEnd);
    imageEl.addEventListener("pointercancel", handlePanEnd);
    closeBtnEl.addEventListener("focus", () => {
        setChromeVisible(true);
        clearChromeHideTimer();
    });
    closeBtnEl.addEventListener("blur", () => {
        scheduleChromeHide();
    });
    imageEl.addEventListener("error", () => {
        if (!isPreviewOpen() || !imageEl.getAttribute("src")) return;
        showPreviewError("Not a supported image");
    });
    imageEl.addEventListener("load", () => {
        // Full image decoded: the info panel can now report real dimensions.
        if (infoOpen) refreshInfoPanel();
    });
    if (window.runtime?.EventsOn) {
        window.runtime.EventsOn("preview_progress", (msgID: any, percent: any) => {
            if (!isPreviewOpen()) return;

            const targetID = Number(msgID);
            if (!Number.isFinite(targetID) || targetID !== activePreviewMsgID) return;

            setPreviewProgress(percent);
        });
    }

    window.addEventListener("keydown", (event) => {
        void handlePreviewKeydown(event);
    }, true);

    return true;
}
