import { get, writable, type Readable } from 'svelte/store';
import {
    getMountStatus,
    mountDrive,
    openMountedDrive,
    safeMountError,
    unmountDrive,
} from '../../api';
import type { MountPhase, MountStatusView } from '../../types';

export interface MountApi {
    mountDrive(): Promise<MountStatusView>;
    mountStatus(): Promise<MountStatusView>;
    openMountedDrive(): Promise<void>;
    unmountDrive(): Promise<MountStatusView>;
}

export interface MountNotice {
    id?: string;
    level: 'info' | 'success' | 'error';
    title: string;
    body?: string;
    sticky?: boolean;
    spinner?: boolean;
}

export interface MountController extends Readable<MountStatusView> {
    refresh(): Promise<void>;
    mount(): Promise<void>;
    open(): Promise<void>;
    disconnect(): Promise<void>;
}

export type MountNotifier = (notice: MountNotice) => unknown;

export const defaultMountApi: MountApi = {
    mountDrive,
    mountStatus: getMountStatus,
    openMountedDrive,
    unmountDrive,
};

const INITIAL_STATUS: MountStatusView = {
    phase: 'idle',
    mounted: false,
    mode: 'read-only',
    label: 'Tdrive personal',
    location: '',
    error: '',
    drive: null,
    windowsDrive: '',
};

function copyStatus(status: MountStatusView): MountStatusView {
    const mounted = Boolean(status.mounted);
    const error = status.error ? safeMountError(status.error) : '';
    const phase: MountPhase = status.phase === 'error' || error
        ? (error ? 'error' : status.phase)
        : status.phase;
    return {
        phase,
        mounted,
        mode: 'read-only',
        label: status.label || 'Tdrive personal',
        location: status.location,
        error,
        drive: status.drive ? { ...status.drive } : null,
        windowsDrive: status.windowsDrive,
    };
}

function withPhase(current: MountStatusView, phase: MountPhase): MountStatusView {
    return { ...current, phase, error: '' };
}

function safeEjectError(value: unknown): string {
    const message = safeMountError(value, 'The drive could not be ejected. Try again.');
    return message.replace(/\bdisconnect(?:ed|ing)?\b/gi, (word) => {
        if (word.toLowerCase() === 'disconnected') return 'ejected';
        if (word.toLowerCase() === 'disconnecting') return 'ejecting';
        return 'eject';
    });
}

export function createMountController(
    api: MountApi = defaultMountApi,
    notify: MountNotifier = () => undefined,
): MountController {
    const state = writable<MountStatusView>(copyStatus(INITIAL_STATUS));
    let revision = 0;
    let mutation: Promise<void> | null = null;
    let refreshRequest: Promise<void> | null = null;
    let openRequest: Promise<void> | null = null;

    function replace(status: MountStatusView): void {
        revision += 1;
        state.set(copyStatus(status));
    }

    function fail(error: unknown, fallback: string, preserveMounted: boolean): void {
        const message = safeMountError(error, fallback);
        revision += 1;
        state.update((current) => ({
            ...current,
            phase: 'error',
            mounted: preserveMounted && current.mounted,
            error: message,
        }));
    }

    function trackMutation(task: () => Promise<void>): Promise<void> {
        const request = task();
        const tracked = request.finally(() => {
            if (mutation === tracked) mutation = null;
        });
        mutation = tracked;
        return tracked;
    }

    function refresh(): Promise<void> {
        if (mutation) return mutation;
        if (refreshRequest) return refreshRequest;
        const startedAtRevision = revision;
        const request = (async () => {
            try {
                const result = await api.mountStatus();
                if (revision === startedAtRevision && !mutation) replace(result);
            } catch (error) {
                if (revision === startedAtRevision && !mutation) {
                    fail(error, 'Could not check the mounted drive. Try again.', get(state).mounted);
                }
            }
        })();
        const tracked = request.finally(() => {
            if (refreshRequest === tracked) refreshRequest = null;
        });
        refreshRequest = tracked;
        return tracked;
    }

    function startMount(): Promise<void> {
        if (mutation) return mutation;
        if (get(state).mounted) return Promise.resolve();
        revision += 1;
        state.update((current) => withPhase(current, 'mounting'));
        notify({
            id: 'mount-drive',
            level: 'info',
            title: `Mounting ${get(state).label}...`,
            sticky: true,
            spinner: true,
        });
        return trackMutation(async () => {
            try {
                const result = await api.mountDrive();
                if (!result.mounted) throw new Error(result.error || 'The drive did not mount.');
                replace(result);
                notify({
                    id: 'mount-drive',
                    level: 'success',
                    title: `${result.label || 'Tdrive personal'} mounted`,
                });
            } catch (error) {
                fail(error, 'The drive could not be mounted. Try again.', false);
                notify({
                    id: 'mount-drive',
                    level: 'error',
                    title: `Could not mount ${get(state).label}`,
                    body: get(state).error,
                });
            }
        });
    }

    function open(): Promise<void> {
        if (!get(state).mounted) return Promise.resolve();
        if (openRequest) return openRequest;
        const request = (async () => {
            try {
                await api.openMountedDrive();
            } catch (error) {
                const message = safeMountError(error, `Could not open ${get(state).label}. Try again.`);
                state.update((current) => ({ ...current, error: message }));
                notify({
                    level: 'error',
                    title: `Could not open ${get(state).label}`,
                    body: message,
                });
            }
        })();
        const tracked = request.finally(() => {
            if (openRequest === tracked) openRequest = null;
        });
        openRequest = tracked;
        return tracked;
    }

    function disconnect(): Promise<void> {
        if (mutation) return mutation;
        if (!get(state).mounted) return Promise.resolve();
        revision += 1;
        state.update((current) => withPhase(current, 'disconnecting'));
        notify({
            id: 'mount-drive',
            level: 'info',
            title: 'Ejecting Tdrive...',
            sticky: true,
            spinner: true,
        });
        return trackMutation(async () => {
            try {
                const result = await api.unmountDrive();
                if (result.mounted) throw new Error(result.error || 'The drive is still mounted.');
                replace(result);
                notify({
                    id: 'mount-drive',
                    level: 'success',
                    title: 'Tdrive ejected',
                });
            } catch (error) {
                const message = safeEjectError(error);
                fail(message, message, true);
                notify({
                    id: 'mount-drive',
                    level: 'error',
                    title: 'Could not eject Tdrive',
                    body: get(state).error,
                });
            }
        });
    }

    return {
        subscribe: state.subscribe,
        refresh,
        mount: startMount,
        open,
        disconnect,
    };
}
