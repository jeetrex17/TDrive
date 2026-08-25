import { get, writable, type Readable } from 'svelte/store';
import {
    getMountStatus,
    mountDrive,
    mountDrives,
    safeMountError,
    unmountDrive,
} from '../../api';
import type { MountableDrive, MountPhase, MountStatusView } from '../../types';
import { mountSelection } from './mount-selection-store';

export interface MountApi {
    mountDrive(): Promise<MountStatusView>;
    mountDrives?(channelIds: readonly number[]): Promise<MountStatusView>;
    mountStatus(): Promise<MountStatusView>;
    unmountDrive(): Promise<MountStatusView>;
    unlockEncryption?(): Promise<boolean>;
}

export interface MountRequestOptions {
    loadDrives?: () => Promise<readonly MountableDrive[]>;
    onAction?: () => void;
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
    loadingDrives: Readable<boolean>;
    refresh(): Promise<void>;
    requestMount(options?: MountRequestOptions): Promise<void>;
    mount(channelIds?: readonly number[]): Promise<void>;
    disconnect(): Promise<void>;
}

export type MountNotifier = (notice: MountNotice) => unknown;

export const defaultMountApi: MountApi = {
    mountDrive,
    mountDrives,
    mountStatus: getMountStatus,
    unmountDrive,
    unlockEncryption: async () => {
        const { requireEncryptionPassword } = await import('../../modules/encryption');
        return requireEncryptionPassword();
    },
};

const INITIAL_STATUS: MountStatusView = {
    phase: 'idle',
    mounted: false,
    mode: 'read-only',
    writeState: 'disabled',
    acceptingWrites: false,
    activeWrites: 0,
    label: 'Tdrive personal',
    location: '',
    error: '',
    drive: null,
};

function withPhase(current: MountStatusView, phase: MountPhase): MountStatusView {
    return { ...current, phase, error: '' };
}

function beginDisconnect(current: MountStatusView): MountStatusView {
    return {
        ...withPhase(current, 'disconnecting'),
        writeState: current.mode === 'read-write' ? 'draining' : 'disabled',
        acceptingWrites: false,
    };
}

function safeEjectError(value: unknown): string {
    const message = safeMountError(value, 'The drive could not be ejected. Try again.');
    return message.replace(/\bdisconnect(?:ed|ing)?\b/gi, (word) => {
        if (word.toLowerCase() === 'disconnected') return 'ejected';
        if (word.toLowerCase() === 'disconnecting') return 'ejecting';
        return 'eject';
    });
}

function markEjectFailed(current: MountStatusView, message: string): MountStatusView {
    const writable = current.mode === 'read-write';
    return {
        ...current,
        phase: 'error',
        mounted: current.mounted,
        writeState: writable ? 'drained' : current.writeState,
        acceptingWrites: writable ? false : current.acceptingWrites,
        activeWrites: writable ? 0 : current.activeWrites,
        error: message,
    };
}

function encryptionPasswordRequired(error: unknown): boolean {
    const message = error instanceof Error ? error.message : String(error ?? '');
    return /encryption password required/i.test(message);
}

function normalizeChannelIds(channelIds: readonly number[] | undefined): number[] | null {
    if (channelIds === undefined) return null;
    return [...new Set(channelIds)]
        .filter((id) => Number.isSafeInteger(id) && id > 0);
}

export function createMountController(
    api: MountApi = defaultMountApi,
    notify: MountNotifier = () => undefined,
): MountController {
    const state = writable<MountStatusView>({ ...INITIAL_STATUS });
    const loadingDrives = writable(false);
    let revision = 0;
    let mutation: Promise<void> | null = null;
    let refreshRequest: Promise<void> | null = null;
    let driveListRequest: Promise<void> | null = null;

    function replace(status: MountStatusView): void {
        revision += 1;
        state.set({
            ...status,
            drive: status.drive ? { ...status.drive } : null,
        });
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

    function startMount(channelIds?: readonly number[]): Promise<void> {
        if (mutation) return mutation;
        if (get(state).mounted) return Promise.resolve();
        const selectedIds = normalizeChannelIds(channelIds);
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
                const requestMount = (): Promise<MountStatusView> => {
                    if (selectedIds === null) return api.mountDrive();
                    if (selectedIds.length === 0) throw new Error('Select at least one drive to mount.');
                    if (!api.mountDrives) throw new Error('Mounting multiple drives is unavailable.');
                    return api.mountDrives([...selectedIds]);
                };
                let result: MountStatusView;
                try {
                    result = await requestMount();
                } catch (error) {
                    if (!encryptionPasswordRequired(error) || !api.unlockEncryption) throw error;
                    const unlocked = await api.unlockEncryption();
                    if (!unlocked) {
                        revision += 1;
                        state.update((current) => ({ ...current, phase: 'idle', mounted: false, error: '' }));
                        notify({
                            id: 'mount-drive',
                            level: 'info',
                            title: 'Mount cancelled',
                            sticky: false,
                            spinner: false,
                        });
                        return;
                    }
                    result = await requestMount();
                }
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

    function requestMount(options: MountRequestOptions = {}): Promise<void> {
        if (driveListRequest) return driveListRequest;
        if (get(state).phase === 'mounting') return mutation ?? Promise.resolve();

        const { loadDrives, onAction = () => undefined } = options;
        if (!loadDrives) {
            onAction();
            return startMount();
        }

        loadingDrives.set(true);
        const request = (async () => {
            try {
                const drives = [...await loadDrives()];
                if (drives.length === 0) throw new Error('No drives are available to mount.');

                onAction();
                if (drives.length === 1) {
                    loadingDrives.set(false);
                    void startMount([drives[0].id]);
                    return;
                }

                mountSelection.open(drives, (channelIds) => {
                    void startMount(channelIds);
                });
            } catch (error) {
                notify({
                    level: 'error',
                    title: 'Could not load drives',
                    body: safeMountError(error, 'The drive list could not be loaded.'),
                });
            }
        })();
        const tracked = request.finally(() => {
            if (driveListRequest === tracked) driveListRequest = null;
            loadingDrives.set(false);
        });
        driveListRequest = tracked;
        return tracked;
    }

    function disconnect(): Promise<void> {
        if (mutation) return mutation;
        if (!get(state).mounted) return Promise.resolve();
        revision += 1;
        state.update(beginDisconnect);
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
                try {
                    // Eject errors do not carry the controller's updated
                    // lifecycle. Re-read it so drained/paused write state and
                    // OS-detach outcomes remain truthful in the UI.
                    replace(await api.mountStatus());
                } catch {
                    // Preserve the last known mounted state when status is
                    // temporarily unavailable; the eject error remains the
                    // actionable failure reported below.
                }
                const message = safeEjectError(error);
                revision += 1;
                state.update((current) => markEjectFailed(current, message));
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
        loadingDrives: { subscribe: loadingDrives.subscribe },
        refresh,
        requestMount,
        mount: startMount,
        disconnect,
    };
}
