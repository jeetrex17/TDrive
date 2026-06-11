import { PreviewFile, PreviewThumbnail } from '../../../wailsjs/go/main/App';
import { state } from '../../state';
import { notify } from '../notifications';

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
    return `file:${Number(item.id || 0)}`;
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
    if (typeof imageEl.animate === "function") {
        imageEl.animate(
            [
                { opacity: 0.84, transform: "scale(0.992)" },
                { opacity: 1, transform: "scale(1)" },
            ],
            {
                duration: 180,
                easing: "cubic-bezier(0.22, 1, 0.36, 1)",
                fill: "both",
            },
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

    let payload;
    try {
        payload = await PreviewFile(msgID);
    } catch (err) {
        if (/encryption password required/i.test(String(err))) {
            const { openEncryptionPasswordModal } = await import('./encryption-password.js');
            const ok = await openEncryptionPasswordModal();
            if (!ok) throw err;
            payload = await PreviewFile(msgID);
        } else {
            throw err;
        }
    }
    const asset = payloadToPreviewAsset(payload);
    await decodePreviewSource(asset.src);
    return asset;
}

export async function loadPreview(target: any) {
    if (!assertPreviewReady()) {
        throw new Error("Preview unavailable");
    }
    const msgID = Number(target?.id || 0);
    const previewKey = getPreviewKey(target);
    const filename = String(target?.name || filenameEl?.textContent || "Preview");
    const token = ++previewRequestToken;
    const previousActivePreviewKey = activePreviewKey;
    const previousActivePreviewMsgID = activePreviewMsgID;
    const previousActivePreviewItem = activePreviewItem;
    activePreviewKey = previewKey;
    activePreviewMsgID = msgID;
    activePreviewItem = target;

    if (!msgID || !previewKey) {
        const err = new Error("Download failed");
        if (token === previewRequestToken && isPreviewOpen()) {
            showPreviewError(err.message);
        }
        throw err;
    }

    let fullShown = false;

    try {
        setPreviewProgress(0);

        // Instant low-res placeholder. The gallery hands us a thumbnail data
        // URL it already loaded (zero extra work); elsewhere we fall back to a
        // server-side thumbnail fetch. Either way the full image replaces it.
        const initialThumb = String(target?.thumbUrl || "");
        if (initialThumb) {
            if (token === previewRequestToken && isPreviewOpen()) {
                showPreviewImage(initialThumb, filename, { keepLoading: true });
            }
        } else {
            void resolveThumbnailPreviewEntry(target)
                .then((asset) => {
                    if (fullShown || token !== previewRequestToken || !isPreviewOpen()) return;
                    if (!asset?.src) return;
                    showPreviewImage(asset.src, filename, { keepLoading: true });
                })
                .catch(() => {});
        }

        const asset = await resolveFullPreviewEntry(target);
        if (token !== previewRequestToken || !isPreviewOpen()) return null;

        if (!asset?.src) {
            throw new Error("Download failed");
        }

        fullShown = true;
        showPreviewImage(asset.src, filename);
        return asset;
    } catch (err) {
        if (token !== previewRequestToken || !isPreviewOpen()) return null;
        const normalized = normalizePreviewError(err);
        const keepCurrentImage = isPreviewVisible() && previousActivePreviewKey === previewKey;
        if (!keepCurrentImage) {
            activePreviewKey = previousActivePreviewKey;
            activePreviewMsgID = previousActivePreviewMsgID;
            activePreviewItem = previousActivePreviewItem;
        }
        showPreviewError(normalized.message, { keepCurrentImage });
        throw normalized;
    }
}

export function closePreviewModal() {
    previewRequestToken += 1;
    clearActivePreview();
    clearChromeHideTimer();

    if (modalEl) {
        modalEl.style.display = "none";
        modalEl.setAttribute("aria-hidden", "true");
        modalEl.classList.remove("is-chrome-visible", "is-preview-error");
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
        event.preventDefault();
        event.stopPropagation();
        void navigatePreview(event.key === "ArrowLeft" ? -1 : 1);
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
    updateNavChrome();

    closeBtnEl.addEventListener("click", closePreviewModal);
    modalEl.addEventListener("click", (event: any) => {
        if (event.target === modalEl || event.target === shellEl || event.target === stageEl) {
            closePreviewModal();
        }
    });
    modalEl.addEventListener("pointermove", () => {
        if (!isPreviewOpen()) return;
        revealChrome();
    });
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
