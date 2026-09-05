const VIDEO_EXTENSIONS = new Set([
    "mp4",
    "m4v",
    "mov",
    "qt",
    "webm",
    "mkv",
    "mk3d",
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

const IMAGE_EXTENSIONS = new Set(["jpg", "jpeg", "png", "gif", "webp", "bmp", "svg"]);

const AUDIO_EXTENSIONS = new Set([
    "mp3",
    "m4a",
    "aac",
    "wav",
    "flac",
    "oga",
    "ogg",
    "opus",
]);

const PDF_EXTENSIONS = new Set(["pdf"]);

const TEXT_EXTENSIONS = new Set([
    "txt",
    "log",
    "md",
    "markdown",
    "csv",
    "tsv",
    "json",
    "yaml",
    "yml",
    "toml",
    "xml",
    "srt",
    "vtt",
]);

// Formats that modern webviews commonly handle directly. The native mpv engine
// will eventually own the wider set above; this list keeps the first in-app
// player honest instead of pretending every container is browser-decodable.
const WEBVIEW_DIRECT_EXTENSIONS = new Set(["mp4", "m4v", "mov", "webm", "ogv"]);

export type FileOpenKind = "image" | "video" | "audio" | "pdf" | "text" | "unsupported";

export function fileExtension(name: string): string {
    const last = String(name || "").trim().split(/[\\/]/).pop() || "";
    const idx = last.lastIndexOf(".");
    if (idx <= 0 || idx === last.length - 1) return "";
    return last.slice(idx + 1).toLowerCase();
}

export function isVideoFile(name: string): boolean {
    return VIDEO_EXTENSIONS.has(fileExtension(name));
}

export function isImageFile(name: string): boolean {
    return IMAGE_EXTENSIONS.has(fileExtension(name));
}

export function isAudioFile(name: string): boolean {
    return AUDIO_EXTENSIONS.has(fileExtension(name));
}

export function isPdfFile(name: string): boolean {
    return PDF_EXTENSIONS.has(fileExtension(name));
}

export function isTextFile(name: string): boolean {
    return TEXT_EXTENSIONS.has(fileExtension(name));
}

export function fileOpenKind(name: string): FileOpenKind {
    const ext = fileExtension(name);
    if (IMAGE_EXTENSIONS.has(ext)) return "image";
    if (VIDEO_EXTENSIONS.has(ext)) return "video";
    if (AUDIO_EXTENSIONS.has(ext)) return "audio";
    if (PDF_EXTENSIONS.has(ext)) return "pdf";
    if (TEXT_EXTENSIONS.has(ext)) return "text";
    return "unsupported";
}

export function isFileOpenable(name: string): boolean {
    return fileOpenKind(name) !== "unsupported";
}

export function isWebviewDirectVideo(name: string): boolean {
    return WEBVIEW_DIRECT_EXTENSIONS.has(fileExtension(name));
}

export function videoFormatLabel(name: string): string {
    const ext = fileExtension(name);
    return ext ? ext.toUpperCase() : "VIDEO";
}

export function fileKindLabel(name: string): string {
    const ext = fileExtension(name);
    if (!ext) return "FILE";
    return ext.toUpperCase();
}
