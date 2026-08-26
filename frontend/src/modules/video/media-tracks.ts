export type NativeTrackType = "audio" | "subtitle";

export interface NativeMediaTrack {
    id: number;
    type: NativeTrackType;
    title?: string;
    language?: string;
    codec?: string;
    selected: boolean;
    default: boolean;
    forced: boolean;
}

const MAX_TRACKS = 128;
const MAX_METADATA_LENGTH = 128;
const MAX_LABEL_PART_LENGTH = 36;

export function normalizeNativeTracks(value: unknown): NativeMediaTrack[] {
    if (!Array.isArray(value)) return [];

    const seen = new Set<string>();
    const tracks: NativeMediaTrack[] = [];
    for (const candidate of value) {
        if (!isRecord(candidate)) continue;
        if (!Number.isSafeInteger(candidate.id) || Number(candidate.id) <= 0) continue;
        if (candidate.type !== "audio" && candidate.type !== "subtitle") continue;

        const id = Number(candidate.id);
        const key = `${candidate.type}:${id}`;
        if (seen.has(key)) continue;
        seen.add(key);

        tracks.push({
            id,
            type: candidate.type,
            title: cleanMetadata(candidate.title),
            language: cleanMetadata(candidate.language),
            codec: cleanMetadata(candidate.codec),
            selected: candidate.selected === true,
            default: candidate.default === true,
            forced: candidate.forced === true,
        });
        if (tracks.length >= MAX_TRACKS) break;
    }
    return tracks;
}

export function nativeTrackLabel(track: NativeMediaTrack, index: number): string {
    const parts = [
        labelPart(track.title),
        labelPart(formatLanguage(track.language)),
        labelPart(track.codec?.toUpperCase()),
    ].filter((part): part is string => Boolean(part));
    const distinct = parts.filter((part, partIndex) => (
        parts.findIndex((candidate) => candidate.toLocaleLowerCase() === part.toLocaleLowerCase()) === partIndex
    ));
    if (distinct.length > 0) return distinct.join(" / ");
    return `${track.type === "audio" ? "Audio" : "Subtitle"} ${index + 1}`;
}

function isRecord(value: unknown): value is Record<string, unknown> {
    return Boolean(value && typeof value === "object" && !Array.isArray(value));
}

function cleanMetadata(value: unknown): string | undefined {
    if (typeof value !== "string") return undefined;
    const cleaned = value.trim().replace(/\s+/g, " ").slice(0, MAX_METADATA_LENGTH);
    return cleaned || undefined;
}

function formatLanguage(value: string | undefined): string | undefined {
    if (!value) return undefined;
    return value.length <= 5 ? value.toUpperCase() : value;
}

function labelPart(value: string | undefined): string | undefined {
    if (!value) return undefined;
    if (value.length <= MAX_LABEL_PART_LENGTH) return value;
    return `${value.slice(0, MAX_LABEL_PART_LENGTH - 3).trimEnd()}...`;
}
