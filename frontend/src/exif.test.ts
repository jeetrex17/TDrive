import { describe, it, expect } from "vitest";
import { parseExif, dataUrlToBytes } from "./modules/exif";

const u16 = (n: number) => [(n >> 8) & 0xff, n & 0xff];
const u32 = (n: number) => [(n >> 24) & 0xff, (n >> 16) & 0xff, (n >> 8) & 0xff, n & 0xff];
const chars = (s: string) => Array.from(s).map((c) => c.charCodeAt(0));

// A hand-built big-endian EXIF JPEG: IFD0 with Make (ASCII at offset) and an
// ExifIFD pointer; ExifIFD with FNumber (RATIONAL at offset) and
// DateTimeOriginal (ASCII at offset). Offsets are laid out so values land in a
// trailing data block, exercising the inline-vs-offset value path.
function syntheticExifJpeg(): Uint8Array {
    const tiff = [
        0x4d, 0x4d, ...u16(0x002a), ...u32(8), // "MM", magic, IFD0 @ 8
        ...u16(2), // IFD0: 2 entries
        0x01, 0x0f, ...u16(2), ...u32(6), ...u32(68), // Make ASCII[6] @ 68
        0x87, 0x69, ...u16(4), ...u32(1), ...u32(38), // ExifIFD pointer @ 38
        ...u32(0), // IFD0 next = none
        ...u16(2), // ExifIFD @ 38: 2 entries
        0x82, 0x9d, ...u16(5), ...u32(1), ...u32(74), // FNumber RATIONAL @ 74
        0x90, 0x03, ...u16(2), ...u32(20), ...u32(82), // DateTimeOriginal ASCII[20] @ 82
        ...u32(0), // ExifIFD next = none
        ...chars("Canon\0"), // @ 68
        ...u32(28), ...u32(10), // @ 74 -> 28/10 = 2.8
        ...chars("2026:06:02 15:42:30\0"), // @ 82
    ];
    const payload = [...chars("Exif\0\0"), ...tiff];
    const jpeg = [0xff, 0xd8, 0xff, 0xe1, ...u16(payload.length + 2), ...payload, 0xff, 0xd9];
    return new Uint8Array(jpeg);
}

describe("parseExif", () => {
    it("reads make, f-number, and capture date", () => {
        const exif = parseExif(syntheticExifJpeg());
        expect(exif.make).toBe("Canon");
        expect(exif.fNumber).toBe(2.8);
        const want = Math.floor(new Date(2026, 5, 2, 15, 42, 30).getTime() / 1000);
        expect(exif.takenTime).toBe(want);
    });

    it("returns empty for a non-JPEG", () => {
        expect(parseExif(new Uint8Array([1, 2, 3, 4]))).toEqual({});
    });

    it("returns empty for a JPEG without EXIF", () => {
        expect(parseExif(new Uint8Array([0xff, 0xd8, 0xff, 0xd9]))).toEqual({});
    });
});

describe("dataUrlToBytes", () => {
    it("decodes a base64 data URL", () => {
        const bytes = dataUrlToBytes("data:image/jpeg;base64,QUJD");
        expect(bytes && Array.from(bytes)).toEqual([65, 66, 67]);
    });

    it("rejects a non-data URL", () => {
        expect(dataUrlToBytes("https://example.com/x.jpg")).toBeNull();
    });
});
