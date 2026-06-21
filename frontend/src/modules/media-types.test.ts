import { describe, expect, it } from "vitest";
import { fileExtension, isVideoFile, isWebviewDirectVideo, videoFormatLabel } from "./media-types";

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
    });
});
