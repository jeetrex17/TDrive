const VIDEO_EXTENSIONS = new Set([
    "mp4",
    "m4v",
    "mov",
    "webm",
    "mkv",
    "avi",
    "ts",
    "m2ts",
    "mts",
    "flv",
    "wmv",
    "ogv",
    "mpeg",
    "mpg",
]);

// Formats that modern webviews commonly handle directly. The native mpv engine
// will eventually own the wider set above; this list keeps the first in-app
// player honest instead of pretending every container is browser-decodable.
const WEBVIEW_DIRECT_EXTENSIONS = new Set(["mp4", "m4v", "mov", "webm", "ogv"]);

export function fileExtension(name: string): string {
    const last = String(name || "").trim().split(/[\\/]/).pop() || "";
    const idx = last.lastIndexOf(".");
    if (idx <= 0 || idx === last.length - 1) return "";
    return last.slice(idx + 1).toLowerCase();
}

export function isVideoFile(name: string): boolean {
    return VIDEO_EXTENSIONS.has(fileExtension(name));
}

export function isWebviewDirectVideo(name: string): boolean {
    return WEBVIEW_DIRECT_EXTENSIONS.has(fileExtension(name));
}

export function videoFormatLabel(name: string): string {
    const ext = fileExtension(name);
    return ext ? ext.toUpperCase() : "VIDEO";
}
