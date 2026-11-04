/** Native screen controls are scoped to one open player, never a backend account. */
export type MobilePlayerOperation = 'begin' | 'state' | 'brightness' | 'rotate' | 'volume' | 'showVolume' | 'hideVolume' | 'end';
export interface MobilePlayerRequest {
    operation: MobilePlayerOperation;
    session: string;
    value?: number;
    orientation?: 'portrait' | 'landscape' | 'auto';
}
export interface MobilePlayerState {
    brightness: number;
    volume: number;
    orientation: 'portrait' | 'landscape';
    volumeMode: 'system' | 'native-slider' | 'unavailable';
}
interface Reply { callbackId: string; result?: unknown; error?: string | null }
interface NativeWindow {
    tdrivePlayer?: { postMessage: (request: string) => void };
    webkit?: { messageHandlers?: { tdrivePlayer?: { postMessage: (request: unknown) => void } } };
    __tdrivePlayerReply?: (reply: Reply) => void;
}
const pending = new Map<string, { resolve: (value: unknown) => void; reject: (error: Error) => void; timer: ReturnType<typeof setTimeout> }>();
let nextCallback = 0;

export function hasMobilePlayerControls(): boolean {
    const host = window as unknown as NativeWindow;
    return typeof host.tdrivePlayer?.postMessage === 'function' || Boolean(host.webkit?.messageHandlers?.tdrivePlayer);
}

export function requestMobilePlayer(request: MobilePlayerRequest): Promise<unknown> {
    const host = window as unknown as NativeWindow;
    if (!hasMobilePlayerControls()) return Promise.reject(new Error('Screen controls are unavailable in this build.'));
    host.__tdrivePlayerReply = (reply) => {
        if (!reply || typeof reply.callbackId !== 'string') return;
        const call = pending.get(reply.callbackId);
        if (!call) return;
        pending.delete(reply.callbackId);
        clearTimeout(call.timer);
        if (reply.error) call.reject(new Error(reply.error));
        else call.resolve(reply.result);
    };
    const callbackId = `player-${++nextCallback}`;
    return new Promise((resolve, reject) => {
        const timer = setTimeout(() => {
            pending.delete(callbackId);
            reject(new Error('Screen controls did not respond.'));
        }, 5000);
        pending.set(callbackId, { resolve, reject, timer });
        try {
            if (host.tdrivePlayer) host.tdrivePlayer.postMessage(JSON.stringify({ callbackId, request }));
            else host.webkit!.messageHandlers!.tdrivePlayer!.postMessage({ callbackId, request });
        } catch (error) {
            clearTimeout(timer);
            pending.delete(callbackId);
            reject(error instanceof Error ? error : new Error('Screen controls are unavailable.'));
        }
    });
}

export function normalizeMobilePlayerState(value: unknown): MobilePlayerState {
    const state = value as Partial<MobilePlayerState> | null;
    if (!state || !Number.isFinite(state.brightness) || !Number.isFinite(state.volume)
        || state.brightness! < 0 || state.brightness! > 1 || state.volume! < 0 || state.volume! > 1
        || !['portrait', 'landscape'].includes(state.orientation ?? '')
        || !['system', 'native-slider', 'unavailable'].includes(state.volumeMode ?? '')) {
        throw new Error('Invalid screen control response.');
    }
    return state as MobilePlayerState;
}
