import Hls from 'hls.js';
import type { NativeMediaTrack } from './media-tracks';

/**
 * Playing the remuxed stream in JavaScript, for the one platform that needs it.
 *
 * Chromium implements no `HTMLMediaElement.audioTracks` at all and extracts no
 * embedded subtitles from Matroska, so on Android a file with more than one
 * soundtrack cannot offer the choice through the element. hls.js parses the
 * master playlist the backend publishes and switches its own renditions, which
 * is the only route to a working picker there.
 *
 * It is not used where it is not needed. Android plays Matroska natively and
 * quickly, and putting a JavaScript demuxer in front of a file with a single
 * soundtrack would cost that for nothing.
 */

// A master playlist with one soundtrack has nothing to pick from, so the native
// path keeps the file. Two or more is the whole reason this module exists.
const SWITCHABLE = 2;

const AUDIO_RENDITION = /^#EXT-X-MEDIA:TYPE=AUDIO/gm;

/**
 * Counts the audio renditions a master playlist offers.
 *
 * Fetching it is what forces the backend to probe the container, so a file it
 * cannot repackage answers with a status rather than a playlist, and the caller
 * falls back to the path that was already working.
 */
export async function audioRenditionCount(masterUrl: string): Promise<number> {
    try {
        const response = await fetch(masterUrl);
        if (!response.ok) return 0;
        const master = await response.text();
        return master.match(AUDIO_RENDITION)?.length ?? 0;
    } catch {
        // A stream that cannot be described is a stream not worth rerouting.
        return 0;
    }
}

export async function prefersJsPlayer(masterUrl: string): Promise<boolean> {
    if (!masterUrl || !Hls.isSupported()) return false;
    return (await audioRenditionCount(masterUrl)) >= SWITCHABLE;
}

export interface HlsSource {
    tracks(): NativeMediaTrack[];
    setAudioTrack(id: number): void;
    setSubtitleTrack(id: number | null): void;
    destroy(): void;
}

/** Track ids are one-based so they never collide with a falsy check. */
function identify(index: number): number {
    return index + 1;
}

function named(label: string | undefined, language: string | undefined, index: number, kind: string) {
    const name = (label || '').trim();
    if (name) return name;
    const code = (language || '').trim();
    if (code) return code.toUpperCase();
    return `${kind} ${index + 1}`;
}

/**
 * Attaches hls.js to an element and returns the handle the pills drive.
 *
 * Resolves once the manifest is parsed, because the track lists are what the
 * caller came for and they do not exist before then. A manifest that never
 * arrives resolves to null rather than hanging, so playback can fall back.
 */
export function attachHls(video: HTMLVideoElement, masterUrl: string, onChange: () => void): Promise<HlsSource | null> {
    return new Promise((resolve) => {
        const hls = new Hls({
            // The stream is on loopback and the segments are muxed on demand, so
            // the usual defensive buffering just delays the first frame.
            maxBufferLength: 30,
            enableWorker: true,
        });
        let settled = false;

        const finish = (source: HlsSource | null) => {
            if (settled) return;
            settled = true;
            if (source === null) hls.destroy();
            resolve(source);
        };

        hls.on(Hls.Events.MANIFEST_PARSED, () => {
            finish({
                tracks: () => [
                    ...hls.audioTracks.map((track, index) => ({
                        id: identify(index),
                        type: 'audio' as const,
                        title: named(track.name, track.lang, index, 'Audio'),
                        language: track.lang || undefined,
                        selected: index === hls.audioTrack,
                        default: Boolean(track.default),
                        forced: false,
                    })),
                    ...hls.subtitleTracks.map((track, index) => ({
                        id: identify(index),
                        type: 'subtitle' as const,
                        title: named(track.name, track.lang, index, 'Subtitle'),
                        language: track.lang || undefined,
                        selected: index === hls.subtitleTrack,
                        default: Boolean(track.default),
                        forced: Boolean(track.forced),
                    })),
                ],
                setAudioTrack: (id) => {
                    hls.audioTrack = id - 1;
                    onChange();
                },
                setSubtitleTrack: (id) => {
                    // hls.js turns subtitles off with -1, which is a real choice
                    // rather than the absence of one.
                    hls.subtitleTrack = id === null ? -1 : id - 1;
                    onChange();
                },
                destroy: () => hls.destroy(),
            });
        });

        for (const event of [Hls.Events.AUDIO_TRACK_SWITCHED, Hls.Events.SUBTITLE_TRACK_SWITCH]) {
            hls.on(event, onChange);
        }

        hls.on(Hls.Events.ERROR, (_event, data) => {
            // Only a fatal error means this route cannot carry the stream; the
            // rest are the recoveries hls.js makes on its own.
            if (data.fatal) finish(null);
        });

        hls.attachMedia(video);
        hls.loadSource(masterUrl);
    });
}
