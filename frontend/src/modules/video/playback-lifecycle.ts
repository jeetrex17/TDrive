export const HTML_MEDIA_ERROR = {
    aborted: 1,
    network: 2,
    decode: 3,
    sourceNotSupported: 4,
} as const;

export interface PlaybackIntentSource {
    paused: boolean;
    currentTime: number;
    volume: number;
    muted: boolean;
    rate: number;
}

export interface PlaybackIntent {
    paused: boolean;
    currentTime: number;
    volume: number;
    muted: boolean;
    rate: number;
}

export function shouldFallbackFromHtmlMediaError(code: number | null | undefined): boolean {
    return code === HTML_MEDIA_ERROR.decode || code === HTML_MEDIA_ERROR.sourceNotSupported;
}

export function capturePlaybackIntent(source: PlaybackIntentSource, paused: boolean): PlaybackIntent {
    return {
        paused,
        currentTime: finiteInRange(source.currentTime, 0, Number.MAX_SAFE_INTEGER, 0),
        volume: finiteInRange(source.volume, 0, 1, 1),
        muted: Boolean(source.muted),
        rate: finiteInRange(source.rate, 0.25, 4, 1),
    };
}

function finiteInRange(value: number, min: number, max: number, fallback: number): number {
    if (!Number.isFinite(value)) return fallback;
    return Math.max(min, Math.min(max, value));
}

type TransitionOperation = (isCurrent: () => boolean) => void | Promise<void>;

/**
 * Serializes player ownership changes while letting each async step verify that
 * a newer open/close request has not superseded it.
 */
export class SerialPlaybackTransitions {
    private generation = 0;
    private tail: Promise<void> = Promise.resolve();

    begin(): number {
        this.generation += 1;
        return this.generation;
    }

    isCurrent(generation: number): boolean {
        return generation === this.generation;
    }

    run(generation: number, operation: TransitionOperation): Promise<void> {
        const result = this.tail.then(async () => {
            if (!this.isCurrent(generation)) return;
            await operation(() => this.isCurrent(generation));
        });
        this.tail = result.catch(() => undefined);
        return result;
    }
}
