import { PreviewFile, PreviewThumbnail } from '../../../wailsjs/go/main/App';
import { state } from '../../state.js';

const SUPPORTED_EXTENSIONS = new Set(["jpg", "jpeg", "png", "gif", "webp", "bmp", "svg"]);
const PREVIEW_CACHE_MAX_ITEMS = 3;
const PREVIEW_CACHE_MAX_BYTES = 30 * 1024 * 1024;
const PREVIEW_CHROME_HIDE_DELAY_MS = 1600;
const PREVIEW_SELECTION_PREFETCH_DELAY_MS = 180;
const REQUIRED_ELEMENT_IDS = [
    "preview-modal",
    "preview-shell",
    "preview-stage",
    "preview-filename",
    "preview-image",
    "preview-loading",
    "preview-error",
    "preview-close",
];

let modalEl = null;
let shellEl = null;
let stageEl = null;
let filenameEl = null;
let imageEl = null;
let loadingEl = null;
let errorEl = null;
let closeBtnEl = null;
let previewReady = false;
let previewRequestToken = 0;
let activePreviewKey = "";
let statusResetTimer = null;
let chromeHideTimer = null;
let selectionPrefetchTimer = null;
let selectionPrefetchKey = "";
let previewCacheBytes = 0;
const previewCache = new Map();
const inflightThumbnailLoads = new Map();
const inflightFullLoads = new Map();

function isSpaceKey(event) {
    return event.code === "Space" || event.key === " " || event.key === "Spacebar";
}

function isTypingContext(element) {
    if (!element) return false;
    const tag = String(element.tagName || "").toUpperCase();
    return element.isContentEditable || tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || tag === "BUTTON";
}

function isBlockingOverlayOpen() {
    const overlays = Array.from(document.querySelectorAll(".modal-overlay"));
    if (overlays.some((el) => el.id !== "preview-modal" && el.style.display !== "none")) {
        return true;
    }

    const contextMenu = document.getElementById("context-menu");
    return Boolean(contextMenu && contextMenu.style.display !== "none");
}

function flashStatus(message, delay = 2200) {
    const statusEl = document.getElementById("status-msg");
    if (!statusEl || !message) return;

    const previous = statusEl.textContent || "Ready";
    statusEl.textContent = message;

    if (statusResetTimer) clearTimeout(statusResetTimer);
    statusResetTimer = setTimeout(() => {
        if (statusEl.textContent === message) {
            statusEl.textContent = previous === message ? "Ready" : previous;
        }
    }, delay);
}

function getPreviewKey(item) {
    if (!item || item.type !== "file") return "";
    return `file:${Number(item.id || 0)}`;
}

function clearActivePreview() {
    activePreviewKey = "";
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

function setChromeVisible(visible) {
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
    imageEl.alt = "Preview";
}

function preparePreviewSurface(filename, { keepCurrentImage = false } = {}) {
    if (!modalEl || !filenameEl || !loadingEl || !errorEl) return;
    if (!keepCurrentImage) {
        filenameEl.textContent = filename || "Preview";
    }
    loadingEl.style.display = "none";
    errorEl.style.display = "none";
    errorEl.textContent = "";
    modalEl.classList.remove("is-preview-error");

    if (!keepCurrentImage) resetImageSurface();
    if (imageEl && !keepCurrentImage) imageEl.alt = filename || "Preview";
}

function showPreviewError(message, { keepCurrentImage = false } = {}) {
    if (!modalEl || !loadingEl || !errorEl) return;

    loadingEl.style.display = "none";
    if (!keepCurrentImage) resetImageSurface();
    errorEl.textContent = message || "Download failed";
    errorEl.style.display = "block";
    modalEl.classList.add("is-preview-error");
    setChromeVisible(true);
    clearChromeHideTimer();
}

function showPreviewImage(src, alt) {
    if (!modalEl || !filenameEl || !imageEl || !loadingEl || !errorEl) return;

    loadingEl.style.display = "none";
    errorEl.style.display = "none";
    errorEl.textContent = "";
    modalEl.classList.remove("is-preview-error");
    filenameEl.textContent = alt || "Preview";
    imageEl.alt = alt || "Preview";
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

function normalizePreviewError(err) {
    if (err instanceof Error && err.message.trim()) return err;
    if (typeof err === "string" && err.trim()) return new Error(err.trim());
    if (err && typeof err.message === "string" && err.message.trim()) return new Error(err.message.trim());
    return new Error("Download failed");
}

function showSelectionPreviewError(selection) {
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

export function isPreviewableImage(filename) {
    const name = String(filename || "").trim();
    const dot = name.lastIndexOf(".");
    if (dot < 0 || dot === name.length - 1) return false;
    return SUPPORTED_EXTENSIONS.has(name.slice(dot + 1).toLowerCase());
}

function estimatePreviewCacheEntryBytes(mimeType, dataBase64) {
    return String(mimeType || "").length + String(dataBase64 || "").length + 64;
}

function getPreviewCacheEntryBytes(entry) {
    if (!entry) return 0;
    return Number(entry.thumbBytes || 0) + Number(entry.fullBytes || 0);
}

function deletePreviewCacheEntry(previewKey) {
    const existing = previewCache.get(previewKey);
    if (!existing) return;
    previewCacheBytes = Math.max(0, previewCacheBytes - getPreviewCacheEntryBytes(existing));
    previewCache.delete(previewKey);
}

function touchPreviewCacheEntry(previewKey, entry) {
    previewCache.delete(previewKey);
    previewCache.set(previewKey, entry);
    return entry;
}

function rememberPreviewCacheEntry(previewKey, partial) {
    const existing = previewCache.get(previewKey) || {};
    const nextEntry = { ...existing, ...partial };
    const previousBytes = getPreviewCacheEntryBytes(existing);

    previewCache.delete(previewKey);
    previewCache.set(previewKey, nextEntry);
    previewCacheBytes += getPreviewCacheEntryBytes(nextEntry) - previousBytes;

    while (previewCache.size > PREVIEW_CACHE_MAX_ITEMS || previewCacheBytes > PREVIEW_CACHE_MAX_BYTES) {
        const oldestKey = previewCache.keys().next().value;
        if (!oldestKey) break;
        deletePreviewCacheEntry(oldestKey);
    }

    return previewCache.get(previewKey) || nextEntry;
}

function getCachedPreviewEntry(previewKey) {
    const existing = previewCache.get(previewKey);
    if (!existing) return null;
    return touchPreviewCacheEntry(previewKey, existing);
}

function buildPreviewSource(mimeType, dataBase64) {
    return `data:${mimeType};base64,${dataBase64}`;
}

function payloadToPreviewAsset(payload) {
    const dataBase64 = String(payload?.data_base64 || "");
    const mimeType = String(payload?.mime_type || "");
    if (!dataBase64 || !mimeType) {
        throw new Error("Download failed");
    }

    return {
        src: buildPreviewSource(mimeType, dataBase64),
        mimeType,
        bytes: estimatePreviewCacheEntryBytes(mimeType, dataBase64),
    };
}

async function decodePreviewSource(src) {
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

function cancelSelectionPrefetch() {
    if (selectionPrefetchTimer) {
        clearTimeout(selectionPrefetchTimer);
        selectionPrefetchTimer = null;
    }
    selectionPrefetchKey = "";
}

async function resolveThumbnailPreviewEntry(target) {
    const msgID = Number(target?.id || 0);
    const previewKey = getPreviewKey(target);
    if (!msgID || !previewKey) {
        throw new Error("Download failed");
    }

    const cached = getCachedPreviewEntry(previewKey);
    if (cached?.thumbSrc || cached?.fullSrc) {
        try {
            await decodePreviewSource(cached.fullSrc || cached.thumbSrc);
            return cached;
        } catch {
            deletePreviewCacheEntry(previewKey);
        }
    }

    const pending = inflightThumbnailLoads.get(previewKey);
    if (pending) {
        return pending;
    }

    const promise = (async () => {
        const asset = payloadToPreviewAsset(await PreviewThumbnail(msgID));
        await decodePreviewSource(asset.src);
        return rememberPreviewCacheEntry(previewKey, {
            thumbSrc: asset.src,
            thumbMimeType: asset.mimeType,
            thumbBytes: asset.bytes,
        });
    })().finally(() => {
        inflightThumbnailLoads.delete(previewKey);
    });

    inflightThumbnailLoads.set(previewKey, promise);
    return promise;
}

async function resolveFullPreviewEntry(target) {
    const msgID = Number(target?.id || 0);
    const previewKey = getPreviewKey(target);
    if (!msgID || !previewKey) {
        throw new Error("Download failed");
    }

    const cached = getCachedPreviewEntry(previewKey);
    if (cached?.fullSrc) {
        try {
            await decodePreviewSource(cached.fullSrc);
            return cached;
        } catch {
            deletePreviewCacheEntry(previewKey);
        }
    }

    const pending = inflightFullLoads.get(previewKey);
    if (pending) {
        return pending;
    }

    const promise = (async () => {
        const asset = payloadToPreviewAsset(await PreviewFile(msgID));
        await decodePreviewSource(asset.src);
        return rememberPreviewCacheEntry(previewKey, {
            fullSrc: asset.src,
            fullMimeType: asset.mimeType,
            fullBytes: asset.bytes,
        });
    })().finally(() => {
        inflightFullLoads.delete(previewKey);
    });

    inflightFullLoads.set(previewKey, promise);
    return promise;
}

async function prefetchPreviewThumbnail(target) {
    if (!target || !isPreviewableImage(target.name)) return null;

    try {
        return await resolveThumbnailPreviewEntry(target);
    } catch {
        return null;
    }
}

function scheduleSelectionPrefetch() {
    cancelSelectionPrefetch();

    const selection = getSelectedPreviewTarget();
    if (selection.reason !== "ok") return;

    const cached = getCachedPreviewEntry(selection.key);
    if (cached?.thumbSrc || cached?.fullSrc) return;

    selectionPrefetchKey = selection.key;
    selectionPrefetchTimer = setTimeout(() => {
        selectionPrefetchTimer = null;
        const current = getSelectedPreviewTarget();
        if (current.reason !== "ok" || current.key !== selectionPrefetchKey) return;
        void prefetchPreviewThumbnail(current.item);
    }, PREVIEW_SELECTION_PREFETCH_DELAY_MS);
}

export async function loadPreview(target) {
    if (!assertPreviewReady()) {
        throw new Error("Preview unavailable");
    }
    const msgID = Number(target?.id || 0);
    const previewKey = getPreviewKey(target);
    const filename = String(target?.name || filenameEl?.textContent || "Preview");
    const token = ++previewRequestToken;
    const previousActivePreviewKey = activePreviewKey;
    activePreviewKey = previewKey;

    if (!msgID || !previewKey) {
        const err = new Error("Download failed");
        if (token === previewRequestToken && isPreviewOpen()) {
            showPreviewError(err.message);
        }
        throw err;
    }

    let fullShown = false;

    try {
        const cached = getCachedPreviewEntry(previewKey);
        if (cached?.fullSrc) {
            try {
                await decodePreviewSource(cached.fullSrc);
                if (token !== previewRequestToken || !isPreviewOpen()) return null;
                fullShown = true;
                showPreviewImage(cached.fullSrc, filename);
                return cached;
            } catch {
                deletePreviewCacheEntry(previewKey);
            }
        }

        if (cached?.thumbSrc) {
            try {
                await decodePreviewSource(cached.thumbSrc);
                if (token !== previewRequestToken || !isPreviewOpen()) return null;
                showPreviewImage(cached.thumbSrc, filename);
            } catch {
                deletePreviewCacheEntry(previewKey);
            }
        } else {
            void resolveThumbnailPreviewEntry(target)
                .then((entry) => {
                    if (fullShown || token !== previewRequestToken || !isPreviewOpen()) return;
                    const previewSrc = entry.thumbSrc || entry.fullSrc || "";
                    if (!previewSrc) return;
                    showPreviewImage(previewSrc, filename);
                })
                .catch(() => {});
        }

        const entry = await resolveFullPreviewEntry(target);
        if (token !== previewRequestToken || !isPreviewOpen()) return null;

        const fullSrc = entry.fullSrc || entry.thumbSrc || "";
        if (!fullSrc) {
            throw new Error("Download failed");
        }

        fullShown = true;
        showPreviewImage(fullSrc, filename);
        return entry;
    } catch (err) {
        if (token !== previewRequestToken || !isPreviewOpen()) return null;
        const normalized = normalizePreviewError(err);
        const keepCurrentImage = isPreviewVisible() && previousActivePreviewKey === previewKey;
        if (!keepCurrentImage) {
            activePreviewKey = previousActivePreviewKey;
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
    resetImageSurface();
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

    const item = selection.item;
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

async function handlePreviewKeydown(event) {
    const spacePressed = isSpaceKey(event);
    const previewOpen = isPreviewOpen();

    if (event.key === "Escape" && previewOpen) {
        event.preventDefault();
        event.stopPropagation();
        closePreviewModal();
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
    errorEl = document.getElementById("preview-error");
    closeBtnEl = document.getElementById("preview-close");
    previewReady = true;

    closeBtnEl.addEventListener("click", closePreviewModal);
    modalEl.addEventListener("click", (event) => {
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

    window.addEventListener("keydown", (event) => {
        void handlePreviewKeydown(event);
    }, true);
    window.addEventListener("tdrive:selectionchange", scheduleSelectionPrefetch);

    return true;
}
