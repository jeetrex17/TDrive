export type UnknownRecord = Record<string, unknown>;

export function asRecord(value: unknown): UnknownRecord {
    return value !== null && typeof value === "object" ? value as UnknownRecord : {};
}

export function boundedText(value: unknown, maxLength: number): string {
    if (typeof value !== "string") return "";
    return [...value]
        .filter((character) => {
            const codePoint = character.codePointAt(0) ?? 0;
            return codePoint > 31 && codePoint !== 127;
        })
        .join("")
        .trim()
        .slice(0, maxLength);
}

export function finiteNumber(value: unknown, fallback = 0): number {
    const number = Number(value);
    return Number.isFinite(number) ? number : fallback;
}

export function nonNegativeNumber(value: unknown): number {
    return Math.max(0, finiteNumber(value));
}
