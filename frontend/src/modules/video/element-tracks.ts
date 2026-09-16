import type { NativeMediaTrack } from './media-tracks';

/**
 * The media element's own track lists are the only way to see inside a stream
 * the webview demuxed for itself.
 *
 * WebKit fills these for HLS, which is what gives a remuxed Matroska the same
 * audio and subtitle pills the native player shows. Chromium does not implement
 * `audioTracks` at all, so this reports what it can and the pills stay hidden
 * where there is nothing to report.
 *
 * `audioTracks` and `textTracks` are absent from the standard DOM typings, so
 * the shapes below are the parts of the specification actually relied on.
 */
interface ElementTrack {
    readonly kind?: string;
    readonly label?: string;
    readonly language?: string;
}

interface ElementAudioTrack extends ElementTrack {
    enabled: boolean;
}

interface ElementTextTrack extends ElementTrack {
    mode: TextTrackMode;
}

interface TrackList<T> {
    readonly length: number;
    [index: number]: T;
    addEventListener?(type: string, listener: () => void): void;
    removeEventListener?(type: string, listener: () => void): void;
}

interface TrackedElement {
    audioTracks?: TrackList<ElementAudioTrack>;
    textTracks?: TrackList<ElementTextTrack>;
}

// Kinds worth offering. A metadata or chapters track is machinery rather than
// something a viewer would ever choose, and WebKit leaves kind empty on tracks
// it took from an HLS rendition.
const SUBTITLE_KINDS = new Set(['subtitles', 'captions', 'forced', '']);

function listOf<T>(list: TrackList<T> | undefined): T[] {
    if (!list || typeof list.length !== 'number') return [];
    const items: T[] = [];
    for (let i = 0; i < list.length; i += 1) {
        const item = list[i];
        if (item) items.push(item);
    }
    return items;
}

// Track ids come from the position in the list rather than from the element,
// because a track's own id is a string that is usually empty. Position is
// stable for as long as the element holds the stream, which is the only span
// these ids are used across.
function identify(index: number): number {
    return index + 1;
}

export function elementAudioTracks(video: HTMLVideoElement): NativeMediaTrack[] {
    return listOf((video as TrackedElement).audioTracks).map((track, index) => ({
        id: identify(index),
        type: 'audio' as const,
        title: track.label || undefined,
        language: track.language || undefined,
        selected: track.enabled === true,
        default: index === 0,
        forced: false,
    }));
}

export function elementTextTracks(video: HTMLVideoElement): NativeMediaTrack[] {
    const tracks: NativeMediaTrack[] = [];
    listOf((video as TrackedElement).textTracks).forEach((track, index) => {
        if (!SUBTITLE_KINDS.has(track.kind ?? '')) return;
        tracks.push({
            id: identify(index),
            type: 'subtitle',
            title: track.label || undefined,
            language: track.language || undefined,
            selected: track.mode === 'showing',
            default: false,
            forced: track.kind === 'forced',
        });
    });
    return tracks;
}

export function elementTracks(video: HTMLVideoElement): NativeMediaTrack[] {
    return [...elementAudioTracks(video), ...elementTextTracks(video)];
}

/**
 * Enabling one audio track has to disable the others in the same pass.
 * The specification allows a list to have several enabled at once, and a
 * player handed two live audio tracks mixes them together.
 */
export function selectElementAudioTrack(video: HTMLVideoElement, id: number): void {
    listOf((video as TrackedElement).audioTracks).forEach((track, index) => {
        track.enabled = identify(index) === id;
    });
}

// A null id turns subtitles off, which is a real choice rather than the absence
// of one, so it is handled here rather than by the caller skipping the call.
export function selectElementTextTrack(video: HTMLVideoElement, id: number | null): void {
    listOf((video as TrackedElement).textTracks).forEach((track, index) => {
        if (!SUBTITLE_KINDS.has(track.kind ?? '')) return;
        track.mode = identify(index) === id ? 'showing' : 'disabled';
    });
}

/**
 * Track lists arrive after the element has parsed enough of the stream to know
 * what is in it, which for HLS is after the playlist and the initialisation
 * segment. Watching them is what makes the pills appear when they do rather
 * than only on the next state change that happens to follow.
 */
export function watchElementTracks(video: HTMLVideoElement, onChange: () => void): () => void {
    const lists = [(video as TrackedElement).audioTracks, (video as TrackedElement).textTracks];
    const events = ['addtrack', 'removetrack', 'change'];
    const removals: Array<() => void> = [];
    for (const list of lists) {
        if (!list?.addEventListener || !list.removeEventListener) continue;
        for (const event of events) {
            list.addEventListener(event, onChange);
            removals.push(() => list.removeEventListener?.(event, onChange));
        }
    }
    return () => {
        for (const remove of removals.splice(0)) remove();
    };
}
