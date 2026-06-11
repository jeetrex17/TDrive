// Builds the lightbox info panel (the Apple-style "ⓘ" card). Pure rendering:
// given the active item, the loaded full-image data URL, and the displayed
// dimensions, it parses EXIF and returns the panel's inner HTML. Every dynamic
// string is escaped; sections that have no data are omitted.

import { escapeHtml, formatBytes, splitNameAndExt } from '../../utils';
import { state } from '../../state';
import { parseExif, dataUrlToBytes, type ImageExif } from '../exif';

export interface InfoInput {
    item: any; // { id, name, size, encrypted?, uploaderId?, uploadTime? }
    fullSrc: string; // data URL of the full image, or "" if not loaded yet
    naturalWidth: number;
    naturalHeight: number;
}

export function renderImageInfoHTML(input: InfoInput): string {
    const { item, fullSrc, naturalWidth, naturalHeight } = input;
    const exif: ImageExif = fullSrc ? exifFromDataUrl(fullSrc) : {};

    const when = exif.takenTime || Number(item?.uploadTime || 0);
    const width = exif.width || naturalWidth || 0;
    const height = exif.height || naturalHeight || 0;

    const sections: string[] = [];

    if (when > 0) {
        sections.push(`<div class="info-date">${escapeHtml(formatWhen(when))}</div>`);
    }

    const details: string[] = [];
    if (width > 0 && height > 0) {
        details.push(row('Dimensions', `${width} × ${height}${megapixels(width, height)}`));
    }
    if (Number(item?.size || 0) > 0) {
        details.push(row('Size', formatBytes(Number(item.size))));
    }
    const ext = splitNameAndExt(String(item?.name || '')).ext;
    if (ext && ext !== 'FILE') {
        details.push(row('Type', ext));
    }
    const uploader = uploaderName(item);
    if (uploader) {
        details.push(row('Uploaded by', uploader));
    }
    if (item?.encrypted) {
        details.push(row('Encrypted', 'Yes'));
    }
    if (details.length) {
        sections.push(section('Details', details.join('')));
    }

    const camera = cameraBlock(exif);
    if (camera) sections.push(section('Camera', camera));

    if (exif.gps) {
        const { lat, lon } = exif.gps;
        // Keyless Google Maps embed: the classic output=embed iframe needs no
        // API key (only the official Maps Embed API does). q=lat,lon centers it
        // and drops a marker.
        const embed = `https://maps.google.com/maps?q=${lat}%2C${lon}&z=14&output=embed`;
        const open = `https://www.google.com/maps/search/?api=1&query=${lat}%2C${lon}`;
        const body = row('Coordinates', escapeHtml(`${lat}, ${lon}`))
            + `<iframe class="info-map" loading="lazy" referrerpolicy="no-referrer" title="Map location" src="${escapeHtml(embed)}"></iframe>`
            + `<button class="info-map-link" type="button" data-map-url="${escapeHtml(open)}">Open in Google Maps</button>`;
        sections.push(section('Location', body));
    }

    if (item?.name) {
        sections.push(`<div class="info-filename" title="${escapeHtml(String(item.name))}">${escapeHtml(String(item.name))}</div>`);
    }

    if (!sections.length) {
        return `<div class="info-empty">No details available.</div>`;
    }
    return sections.join('');
}

function exifFromDataUrl(dataUrl: string): ImageExif {
    const bytes = dataUrlToBytes(dataUrl);
    return bytes ? parseExif(bytes) : {};
}

function cameraBlock(exif: ImageExif): string {
    const rows: string[] = [];

    const device = [exif.make, exif.model].filter(Boolean).join(' ').trim();
    if (device) rows.push(row('Camera', escapeHtml(dedupeMake(exif.make, exif.model) || device)));
    if (exif.lens) rows.push(row('Lens', escapeHtml(exif.lens)));

    const settings = [
        exif.fNumber ? `ƒ${exif.fNumber}` : '',
        exif.exposureTime || '',
        exif.focalLength ? `${exif.focalLength} mm` : '',
        exif.iso ? `ISO ${exif.iso}` : '',
    ].filter(Boolean);
    if (settings.length) rows.push(row('Exposure', escapeHtml(settings.join(' · '))));

    return rows.join('');
}

// Camera make is often a prefix of the model ("Apple" + "Apple iPhone 14"),
// so collapse the redundancy the way Apple Photos does.
function dedupeMake(make?: string, model?: string): string {
    const mk = (make || '').trim();
    const md = (model || '').trim();
    if (!mk) return md;
    if (!md) return mk;
    if (md.toLowerCase().startsWith(mk.toLowerCase())) return md;
    return `${mk} ${md}`;
}

function uploaderName(item: any): string {
    if (state.activeChannel?.kind !== 'shared') return '';
    const id = Number(item?.uploaderId || 0);
    if (id <= 0) return '';
    return escapeHtml(state.userNames.get(String(id)) || '');
}

function megapixels(w: number, h: number): string {
    const mp = (w * h) / 1_000_000;
    if (mp < 0.1) return '';
    return ` · ${Math.round(mp * 10) / 10} MP`;
}

function formatWhen(unixSec: number): string {
    const d = new Date(unixSec * 1000);
    const date = d.toLocaleDateString(undefined, { weekday: 'long', month: 'long', day: 'numeric', year: 'numeric' });
    const time = d.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
    return `${date} · ${time}`;
}

function section(title: string, body: string): string {
    return `<div class="info-section"><div class="info-section-title">${escapeHtml(title)}</div>${body}</div>`;
}

// row builds a label/value line. The value is inserted as-is, so callers escape
// any dynamic content before passing it in.
function row(label: string, value: string): string {
    return `<div class="info-row"><span class="info-label">${escapeHtml(label)}</span><span class="info-value">${value}</span></div>`;
}
