// Minimal EXIF reader for the photo info panel. It pulls just the tags an
// Apple-style info card shows from a JPEG's APP1 block (capture date, camera,
// exposure, dimensions, GPS) and returns whatever it can; any malformed or
// missing field is simply absent. It never throws: callers render whatever is
// present. The lightbox already holds the full image bytes, so parsing here
// costs nothing extra.

export interface ImageExif {
    takenTime?: number; // unix seconds, from DateTimeOriginal
    make?: string;
    model?: string;
    lens?: string;
    fNumber?: number;
    exposureTime?: string; // pretty, e.g. "1/120" or "2s"
    iso?: number;
    focalLength?: number; // mm
    width?: number; // PixelXDimension
    height?: number; // PixelYDimension
    gps?: { lat: number; lon: number };
}

// EXIF tag numbers we read.
const TAG_MAKE = 0x010f;
const TAG_MODEL = 0x0110;
const TAG_EXIF_IFD = 0x8769;
const TAG_GPS_IFD = 0x8825;
const TAG_EXPOSURE_TIME = 0x829a;
const TAG_FNUMBER = 0x829d;
const TAG_ISO = 0x8827;
const TAG_DATETIME_ORIGINAL = 0x9003;
const TAG_FOCAL_LENGTH = 0x920a;
const TAG_PIXEL_X = 0xa002;
const TAG_PIXEL_Y = 0xa003;
const TAG_LENS_MODEL = 0xa434;

const TAG_GPS_LAT_REF = 0x0001;
const TAG_GPS_LAT = 0x0002;
const TAG_GPS_LON_REF = 0x0003;
const TAG_GPS_LON = 0x0004;

// Bytes per EXIF/TIFF field type, indexed by type id. 0 marks types we ignore.
const TYPE_SIZE: Record<number, number> = { 1: 1, 2: 1, 3: 2, 4: 4, 5: 8, 7: 1, 9: 4, 10: 8 };

/** Decode a base64 data URL into raw bytes, or null if it is not one. */
export function dataUrlToBytes(dataUrl: string): Uint8Array | null {
    const comma = dataUrl.indexOf(",");
    if (comma < 0 || !/;base64/i.test(dataUrl.slice(0, comma))) return null;
    try {
        const bin = atob(dataUrl.slice(comma + 1));
        const out = new Uint8Array(bin.length);
        for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
        return out;
    } catch {
        return null;
    }
}

export function parseExif(bytes: Uint8Array): ImageExif {
    try {
        return parseExifUnsafe(bytes);
    } catch {
        return {};
    }
}

function parseExifUnsafe(bytes: Uint8Array): ImageExif {
    const tiff = findTiffOffset(bytes);
    if (tiff < 0) return {};

    const dv = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
    const little = byteOrder(dv, tiff);
    if (little === null) return {};
    if (u16(dv, tiff + 2, little) !== 0x002a) return {};

    const out: ImageExif = {};
    const ifd0 = tiff + u32(dv, tiff + 4, little);

    let exifIfd = -1;
    let gpsIfd = -1;
    walkIFD(dv, bytes, tiff, ifd0, little, (tag, type, count, valOff) => {
        switch (tag) {
            case TAG_MAKE: out.make = ascii(dv, bytes, tiff, type, count, valOff, little); break;
            case TAG_MODEL: out.model = ascii(dv, bytes, tiff, type, count, valOff, little); break;
            case TAG_EXIF_IFD: exifIfd = tiff + u32(dv, valOff, little); break;
            case TAG_GPS_IFD: gpsIfd = tiff + u32(dv, valOff, little); break;
        }
    });

    if (exifIfd > tiff) {
        walkIFD(dv, bytes, tiff, exifIfd, little, (tag, type, count, valOff) => {
            switch (tag) {
                case TAG_DATETIME_ORIGINAL: out.takenTime = parseExifDate(ascii(dv, bytes, tiff, type, count, valOff, little)); break;
                case TAG_EXPOSURE_TIME: out.exposureTime = prettyExposure(rational(dv, tiff, type, valOff, little)); break;
                case TAG_FNUMBER: out.fNumber = round1(rational(dv, tiff, type, valOff, little)); break;
                case TAG_ISO: out.iso = intVal(dv, type, valOff, little); break;
                case TAG_FOCAL_LENGTH: out.focalLength = round1(rational(dv, tiff, type, valOff, little)); break;
                case TAG_LENS_MODEL: out.lens = ascii(dv, bytes, tiff, type, count, valOff, little); break;
                case TAG_PIXEL_X: out.width = intVal(dv, type, valOff, little); break;
                case TAG_PIXEL_Y: out.height = intVal(dv, type, valOff, little); break;
            }
        });
    }

    if (gpsIfd > tiff) {
        const gps = parseGps(dv, tiff, gpsIfd, little);
        if (gps) out.gps = gps;
    }

    return cleanExif(out);
}

// findTiffOffset returns the byte offset of the TIFF header inside the JPEG's
// EXIF APP1 segment, or -1 if the data is not a JPEG or carries no EXIF.
function findTiffOffset(bytes: Uint8Array): number {
    if (bytes.length < 4 || bytes[0] !== 0xff || bytes[1] !== 0xd8) return -1;
    let i = 2;
    while (i + 4 <= bytes.length) {
        if (bytes[i] !== 0xff) return -1;
        const marker = bytes[i + 1];
        if (marker === 0xd9 || marker === 0xda) return -1; // EOI / start of scan
        const segLen = (bytes[i + 2] << 8) | bytes[i + 3];
        if (segLen < 2) return -1;
        const start = i + 4;
        const end = i + 2 + segLen;
        if (end > bytes.length) return -1;
        if (marker === 0xe1 && start + 6 <= end && asciiEquals(bytes, start, "Exif")) {
            return start + 6; // skip "Exif\0\0"
        }
        i = end;
    }
    return -1;
}

function walkIFD(
    dv: DataView,
    bytes: Uint8Array,
    tiff: number,
    ifdOffset: number,
    little: boolean,
    handler: (tag: number, type: number, count: number, valOff: number) => void,
): void {
    if (ifdOffset < tiff || ifdOffset + 2 > bytes.length) return;
    const count = u16(dv, ifdOffset, little);
    let entry = ifdOffset + 2;
    for (let i = 0; i < count; i++) {
        if (entry + 12 > bytes.length) return;
        handler(u16(dv, entry, little), u16(dv, entry + 2, little), u32(dv, entry + 4, little), entry + 8);
        entry += 12;
    }
}

// valuePtr resolves where a field's data lives: inline in the entry's 4-byte
// value slot when it fits, otherwise at an offset (relative to the TIFF start).
function valuePtr(dv: DataView, tiff: number, type: number, count: number, valOff: number, little: boolean): number {
    const size = (TYPE_SIZE[type] || 1) * count;
    return size <= 4 ? valOff : tiff + u32(dv, valOff, little);
}

function ascii(dv: DataView, bytes: Uint8Array, tiff: number, type: number, count: number, valOff: number, little: boolean): string {
    if (type !== 2 || count <= 0) return "";
    const ptr = valuePtr(dv, tiff, type, count, valOff, little);
    if (ptr < 0 || ptr + count > bytes.length) return "";
    let s = "";
    for (let i = 0; i < count; i++) {
        const c = bytes[ptr + i];
        if (c === 0) break;
        s += String.fromCharCode(c);
    }
    return s.trim();
}

function intVal(dv: DataView, type: number, valOff: number, little: boolean): number {
    if (type === 3) return u16(dv, valOff, little);
    return u32(dv, valOff, little);
}

function rational(dv: DataView, tiff: number, type: number, valOff: number, little: boolean): number {
    if (type !== 5 && type !== 10) return 0;
    const ptr = tiff + u32(dv, valOff, little);
    if (ptr + 8 > dv.byteLength) return 0;
    const num = u32(dv, ptr, little);
    const den = u32(dv, ptr + 4, little);
    return den === 0 ? 0 : num / den;
}

function parseGps(dv: DataView, tiff: number, ifd: number, little: boolean): { lat: number; lon: number } | null {
    let latRef = "";
    let lonRef = "";
    let lat = NaN;
    let lon = NaN;
    // GPS coordinates are three RATIONALs (deg, min, sec); read the IFD directly.
    if (ifd + 2 > dv.byteLength) return null;
    const count = u16(dv, ifd, little);
    let entry = ifd + 2;
    for (let i = 0; i < count; i++) {
        if (entry + 12 > dv.byteLength) break;
        const tag = u16(dv, entry, little);
        const valOff = entry + 8;
        if (tag === TAG_GPS_LAT_REF) latRef = gpsRef(dv, valOff);
        else if (tag === TAG_GPS_LON_REF) lonRef = gpsRef(dv, valOff);
        else if (tag === TAG_GPS_LAT) lat = gpsCoord(dv, tiff, valOff, little);
        else if (tag === TAG_GPS_LON) lon = gpsCoord(dv, tiff, valOff, little);
        entry += 12;
    }
    if (!Number.isFinite(lat) || !Number.isFinite(lon)) return null;
    if (latRef === "S") lat = -lat;
    if (lonRef === "W") lon = -lon;
    return { lat: round5(lat), lon: round5(lon) };
}

function gpsRef(dv: DataView, valOff: number): string {
    return String.fromCharCode(dv.getUint8(valOff)).toUpperCase();
}

function gpsCoord(dv: DataView, tiff: number, valOff: number, little: boolean): number {
    const ptr = tiff + u32(dv, valOff, little);
    if (ptr + 24 > dv.byteLength) return NaN;
    const deg = ratAt(dv, ptr, little);
    const min = ratAt(dv, ptr + 8, little);
    const sec = ratAt(dv, ptr + 16, little);
    return deg + min / 60 + sec / 3600;
}

function ratAt(dv: DataView, ptr: number, little: boolean): number {
    const num = u32(dv, ptr, little);
    const den = u32(dv, ptr + 4, little);
    return den === 0 ? 0 : num / den;
}

// EXIF DateTimeOriginal is "YYYY:MM:DD HH:MM:SS" in local time.
function parseExifDate(s: string): number | undefined {
    const m = /^(\d{4}):(\d{2}):(\d{2})\s+(\d{2}):(\d{2}):(\d{2})/.exec(s);
    if (!m) return undefined;
    const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]), Number(m[4]), Number(m[5]), Number(m[6]));
    const t = Math.floor(d.getTime() / 1000);
    return Number.isFinite(t) && t > 0 ? t : undefined;
}

function prettyExposure(sec: number): string | undefined {
    if (!sec || sec <= 0) return undefined;
    if (sec >= 1) return `${round1(sec)}s`;
    return `1/${Math.round(1 / sec)}`;
}

function cleanExif(e: ImageExif): ImageExif {
    // Drop zero/empty values so the panel only renders real data.
    if (!e.fNumber) delete e.fNumber;
    if (!e.focalLength) delete e.focalLength;
    if (!e.iso) delete e.iso;
    if (!e.width) delete e.width;
    if (!e.height) delete e.height;
    if (!e.make) delete e.make;
    if (!e.model) delete e.model;
    if (!e.lens) delete e.lens;
    return e;
}

function byteOrder(dv: DataView, tiff: number): boolean | null {
    const b0 = dv.getUint8(tiff);
    const b1 = dv.getUint8(tiff + 1);
    if (b0 === 0x49 && b1 === 0x49) return true; // "II" little-endian
    if (b0 === 0x4d && b1 === 0x4d) return false; // "MM" big-endian
    return null;
}

function u16(dv: DataView, off: number, little: boolean): number {
    return dv.getUint16(off, little);
}

function u32(dv: DataView, off: number, little: boolean): number {
    return dv.getUint32(off, little);
}

function asciiEquals(bytes: Uint8Array, off: number, want: string): boolean {
    for (let i = 0; i < want.length; i++) {
        if (bytes[off + i] !== want.charCodeAt(i)) return false;
    }
    return true;
}

function round1(n: number): number {
    return Math.round(n * 10) / 10;
}

function round5(n: number): number {
    return Math.round(n * 100000) / 100000;
}
