import { describe, expect, it } from "vitest";
import {
    fileExtension,
    fileKindLabel,
    fileOpenKind,
    isAudioFile,
    isFileOpenable,
    isImageFile,
    isPdfFile,
    isTextFile,
    isVideoFile,
    isWebviewDirectVideo,
    videoFormatLabel,
} from "./media-types";

describe("media type helpers", () => {
    it("normalizes extensions without trusting path shape", () => {
        expect(fileExtension("movie.MP4")).toBe("mp4");
        expect(fileExtension("/tmp/archive.tar.gz")).toBe("gz");
        expect(fileExtension(".hidden")).toBe("");
        expect(fileExtension("no-extension")).toBe("");
    });

    it("separates all video containers from webview-direct candidates", () => {
        expect(isVideoFile("clip.mkv")).toBe(true);
        expect(isVideoFile("clip.avi")).toBe(true);
        expect(isVideoFile("clip.pdf")).toBe(false);
        expect(isWebviewDirectVideo("clip.mp4")).toBe(true);
        expect(isWebviewDirectVideo("clip.mkv")).toBe(false);
    });

    it("returns compact display labels", () => {
        expect(videoFormatLabel("demo.mov")).toBe("MOV");
        expect(videoFormatLabel("demo")).toBe("VIDEO");
        expect(fileKindLabel("report.pdf")).toBe("PDF");
        expect(fileKindLabel("no-extension")).toBe("FILE");
    });

    it("classifies every in-app opener family", () => {
        expect(fileOpenKind("photo.webp")).toBe("image");
        expect(fileOpenKind("movie.mkv")).toBe("video");
        expect(fileOpenKind("track.FLAC")).toBe("audio");
        expect(fileOpenKind("paper.pdf")).toBe("pdf");
        expect(fileOpenKind("notes.md")).toBe("text");
        expect(fileOpenKind("archive.zip")).toBe("unsupported");
    });

    it("exposes narrow predicates for UI routing", () => {
        expect(isImageFile("scan.png")).toBe(true);
        expect(isAudioFile("song.mp3")).toBe(true);
        expect(isPdfFile("manual.pdf")).toBe(true);
        expect(isTextFile("subtitles.srt")).toBe(true);
        expect(isFileOpenable("unknown.bin")).toBe(false);
        expect(isFileOpenable("notes.txt")).toBe(true);
    });
});
