import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync } from 'svelte';
import { get } from 'svelte/store';

const authApi = vi.hoisted(() => ({
    checkSystemStatus: vi.fn(),
    saveSetup: vi.fn(),
    loginPhoneNumber: vi.fn(),
    submitCode: vi.fn(),
    submitPassword: vi.fn(),
    checkLoginStatus: vi.fn(),
    waitForBackendReady: vi.fn(),
    preparePersonalDrive: vi.fn(),
    discoverPersonalDrives: vi.fn(),
    selectPersonalDrive: vi.fn(),
    createPersonalDrive: vi.fn(),
    getMyUserId: vi.fn(),
    logout: vi.fn(),
    syncChannel: vi.fn(),
}));

const collaborators = vi.hoisted(() => ({
    loadChannels: vi.fn(),
    loadEncryptionStatus: vi.fn(),
    loadSelfUser: vi.fn(),
    renderBreadcrumb: vi.fn(),
    notify: vi.fn(),
    dismissNotification: vi.fn(),
    triggerRefresh: vi.fn(),
}));

vi.mock('../api', () => authApi);
vi.mock('./navigation', () => ({ renderBreadcrumb: collaborators.renderBreadcrumb }));
vi.mock('./channels', () => ({ loadChannels: collaborators.loadChannels }));
vi.mock('./encryption', () => ({ loadEncryptionStatus: collaborators.loadEncryptionStatus }));
vi.mock('./profile-menu', () => ({ loadSelfUser: collaborators.loadSelfUser }));
vi.mock('./renditions/runtime', () => ({ resetRenditions: vi.fn() }));
vi.mock('./app-actions', () => ({ appActions: () => ({ triggerRefresh: collaborators.triggerRefresh }) }));
vi.mock('./notifications', () => ({
    notify: collaborators.notify,
    dismissNotification: collaborators.dismissNotification,
}));

import {
    createPersonalDrive,
    initializeSession,
    preparePersonalDriveAndContinue,
    selectPersonalDrive,
} from './auth';
import { appView, authScreen, showAuthView, showStartupView } from '../ui/app/app-store';
import { personalDriveSetup } from '../ui/auth/personal-drive-store';
import { clearAllFailedUploadHistory, failedUploadHistory, rememberFailedUploadActivity, setFailedUploadHistoryAccount } from '../ui/notifications/failed-upload-history';
import type { UploadActivity } from '../ui/notifications/upload-activity-store';
import { confirmLogout } from './modals/logout';
import { state } from '../state';

function deferred<T>() {
    let resolve!: (value: T) => void;
    let reject!: (reason?: unknown) => void;
    const promise = new Promise<T>((promiseResolve, promiseReject) => {
        resolve = promiseResolve;
        reject = promiseReject;
    });
    return { promise, resolve, reject };
}

beforeEach(() => {
    vi.resetAllMocks();
    const storage = new Map<string, string>();
    vi.stubGlobal('localStorage', {
        get length() { return storage.size; },
        key: (index: number) => [...storage.keys()][index] ?? null,
        getItem: (key: string) => storage.get(key) ?? null,
        setItem: (key: string, value: string) => storage.set(key, value),
        removeItem: (key: string) => storage.delete(key),
    });
    setFailedUploadHistoryAccount(0);
    authApi.getMyUserId.mockResolvedValue(77);
    authApi.waitForBackendReady.mockResolvedValue(undefined);
    authApi.checkSystemStatus.mockResolvedValue('READY');
    authApi.checkLoginStatus.mockResolvedValue(true);
    authApi.syncChannel.mockResolvedValue(undefined);
    collaborators.loadChannels.mockResolvedValue(undefined);
    collaborators.loadEncryptionStatus.mockResolvedValue(undefined);
    showStartupView();
    personalDriveSetup.reset();
});

afterEach(() => {
    clearAllFailedUploadHistory();
    vi.unstubAllGlobals();
});

function failedBatch(name: string): UploadActivity {
    return {
        id: name, name, driveId: 8, driveTitle: 'Personal', destination: 'Personal / Photos',
        encrypt: false, addedAt: 10, startedAt: 11, finishedAt: 12, status: 'failed', fileCount: 1,
        files: [{ index: 0, name: `${name}.jpg`, status: 'failed' }],
    };
}

describe('personal drive startup gate', () => {
    it('loads only the authenticated account’s failed uploads after identity resolves', async () => {
        setFailedUploadHistoryAccount(77);
        rememberFailedUploadActivity(failedBatch('My upload'));
        setFailedUploadHistoryAccount(99);
        rememberFailedUploadActivity(failedBatch('Other account upload'));
        const identity = deferred<number>();
        authApi.getMyUserId.mockReturnValueOnce(identity.promise);
        authApi.preparePersonalDrive.mockResolvedValue({ status: 'ready', activeChannelId: '8200' });

        const startup = initializeSession();
        await vi.waitFor(() => expect(authApi.getMyUserId).toHaveBeenCalledOnce());
        expect(get(failedUploadHistory)).toEqual([]);
        identity.resolve(77);
        await startup;
        expect(get(failedUploadHistory).map((entry) => entry.name)).toEqual(['My upload']);
    });

    it('clears failed upload history before full logout and ignores late failures', async () => {
        setFailedUploadHistoryAccount(77);
        rememberFailedUploadActivity(failedBatch('Earlier upload'));
        const identity = deferred<number>();
        authApi.getMyUserId.mockReturnValueOnce(identity.promise);
        authApi.preparePersonalDrive.mockResolvedValue({ status: 'ready', activeChannelId: '8200' });
        const startup = initializeSession();
        await vi.waitFor(() => expect(authApi.getMyUserId).toHaveBeenCalledOnce());
        const result = deferred<void>();
        authApi.logout.mockReturnValueOnce(result.promise);

        const loggingOut = confirmLogout('full');
        expect(authApi.logout).toHaveBeenCalledWith('full');
        identity.resolve(77);
        await startup;
        expect(state.myUserID).toBe(77);
        expect(get(failedUploadHistory)).toEqual([]);
        rememberFailedUploadActivity(failedBatch('Late upload'));
        result.resolve();
        await loggingOut;

        expect(state.myUserID).toBe(0);
        setFailedUploadHistoryAccount(77);
        expect(get(failedUploadHistory)).toEqual([]);
    });

    it('lets the dashboard load and resumes history for its identity if full logout fails', async () => {
        const identity = deferred<number>();
        authApi.getMyUserId.mockReturnValueOnce(identity.promise);
        authApi.preparePersonalDrive.mockResolvedValue({ status: 'ready', activeChannelId: '8200' });
        const startup = initializeSession();
        await vi.waitFor(() => expect(authApi.getMyUserId).toHaveBeenCalledOnce());
        const result = deferred<void>();
        authApi.logout.mockReturnValueOnce(result.promise);

        const loggingOut = confirmLogout('full');
        identity.resolve(77);
        await startup;
        expect(collaborators.loadEncryptionStatus).toHaveBeenCalledOnce();
        result.reject(new Error('Local cleanup failed'));
        await loggingOut;

        expect(get(appView)).toEqual({ kind: 'dashboard' });
        expect(state.myUserID).toBe(77);
        rememberFailedUploadActivity(failedBatch('New upload'));
        expect(get(failedUploadHistory).map((entry) => entry.name)).toEqual(['New upload']);
    });

    it('keeps startup visible until the backend is ready before checking login', async () => {
        const readiness = deferred<void>();
        authApi.waitForBackendReady.mockReturnValueOnce(readiness.promise);
        authApi.preparePersonalDrive.mockResolvedValue({ status: 'ready', activeChannelId: '8200' });

        const startup = initializeSession();
        await vi.waitFor(() => expect(authApi.waitForBackendReady).toHaveBeenCalledOnce());
        expect(get(appView)).toEqual({ kind: 'startup' });
        expect(authApi.checkSystemStatus).not.toHaveBeenCalled();
        expect(authApi.checkLoginStatus).not.toHaveBeenCalled();
        expect(authApi.preparePersonalDrive).not.toHaveBeenCalled();

        readiness.resolve();
        await startup;
        expect(get(appView)).toEqual({ kind: 'dashboard' });
    });

    it('shows first-time credential setup only after backend readiness', async () => {
        const readiness = deferred<void>();
        authApi.waitForBackendReady.mockReturnValueOnce(readiness.promise);
        authApi.checkSystemStatus.mockResolvedValue('NEEDS_SETUP');

        const startup = initializeSession();
        await vi.waitFor(() => expect(authApi.waitForBackendReady).toHaveBeenCalledOnce());
        expect(get(appView)).toEqual({ kind: 'startup' });
        expect(authApi.checkSystemStatus).not.toHaveBeenCalled();
        readiness.resolve();
        await startup;

        expect(get(authScreen)).toBe('setup');
        expect(authApi.checkLoginStatus).not.toHaveBeenCalled();
    });

    it('waits for backend readiness before a login event opens the saved drive', async () => {
        const readiness = deferred<void>();
        authApi.waitForBackendReady.mockReturnValueOnce(readiness.promise);
        authApi.preparePersonalDrive.mockResolvedValue({ status: 'ready', activeChannelId: '8200' });

        const preparation = preparePersonalDriveAndContinue();
        await vi.waitFor(() => expect(authApi.waitForBackendReady).toHaveBeenCalledOnce());
        expect(authApi.preparePersonalDrive).not.toHaveBeenCalled();
        expect(get(appView)).toEqual({ kind: 'startup' });
        readiness.resolve();
        await preparation;
        expect(get(appView)).toEqual({ kind: 'dashboard' });
    });

    it('rejects startup with the backend initialization cause', async () => {
        authApi.waitForBackendReady.mockRejectedValueOnce(new Error('database permission denied'));

        await expect(initializeSession()).rejects.toThrow('database permission denied');

        expect(authApi.checkLoginStatus).not.toHaveBeenCalled();
        expect(authApi.preparePersonalDrive).not.toHaveBeenCalled();
        expect(get(authScreen)).toBeNull();
    });

    it('shows a startup failure if backend construction fails during login completion', async () => {
        authApi.waitForBackendReady.mockRejectedValueOnce(new Error('database permission denied'));

        await preparePersonalDriveAndContinue();

        expect(get(appView)).toMatchObject({ kind: 'fatal', error: { source: 'startup' } });
        expect(authApi.preparePersonalDrive).not.toHaveBeenCalled();
        expect(authApi.discoverPersonalDrives).not.toHaveBeenCalled();
    });

    it('ignores stale readiness after a newer drive flow finishes', async () => {
        const olderReadiness = deferred<void>();
        authApi.waitForBackendReady.mockReturnValueOnce(olderReadiness.promise);
        authApi.preparePersonalDrive.mockResolvedValue({ status: 'ready', activeChannelId: '8200' });

        const olderFlow = preparePersonalDriveAndContinue();
        await preparePersonalDriveAndContinue();
        olderReadiness.resolve();
        await olderFlow;

        expect(authApi.preparePersonalDrive).toHaveBeenCalledOnce();
        expect(collaborators.loadChannels).toHaveBeenCalledOnce();
        expect(get(appView)).toEqual({ kind: 'dashboard' });
    });
    it('takes the saved-config fast path directly to the dashboard', async () => {
        authApi.preparePersonalDrive.mockResolvedValue({ status: 'ready', activeChannelId: '8200' });
        const screens: unknown[] = [];
        const unsubscribe = authScreen.subscribe((screen) => screens.push(screen));

        await preparePersonalDriveAndContinue();
        unsubscribe();

        expect(authApi.preparePersonalDrive).toHaveBeenCalledOnce();
        expect(authApi.discoverPersonalDrives).not.toHaveBeenCalled();
        expect(screens).not.toContain('drive');
        expect(get(authScreen)).toBeNull();
        expect(get(appView)).toEqual({ kind: 'dashboard' });
        expect(collaborators.loadChannels).toHaveBeenCalledOnce();
    });

    it('keeps the dashboard hidden and shows candidates when selection is required', async () => {
        authApi.preparePersonalDrive.mockResolvedValue({ status: 'selection_required', activeChannelId: '' });
        authApi.discoverPersonalDrives.mockResolvedValue([{
            id: '8200', title: 'TDrive', createdAt: 100,
            hasActivity: true, recommended: true,
        }]);

        await preparePersonalDriveAndContinue();
        flushSync();

        expect(authApi.discoverPersonalDrives).toHaveBeenCalledOnce();
        expect(get(authScreen)).toBe('drive');
        expect(get(personalDriveSetup).candidates).toHaveLength(1);
        expect(authApi.createPersonalDrive).not.toHaveBeenCalled();
        expect(collaborators.loadChannels).not.toHaveBeenCalled();
    });

    it('surfaces discovery failure with its cause and never creates', async () => {
        authApi.preparePersonalDrive.mockResolvedValue({ status: 'selection_required', activeChannelId: '' });
        authApi.discoverPersonalDrives.mockRejectedValue('rpc error code 420: FLOOD_WAIT_30');

        await preparePersonalDriveAndContinue();

        expect(get(authScreen)).toBe('drive');
        expect(get(personalDriveSetup)).toMatchObject({
            phase: 'discovery-error',
            error: 'Could not look up your Telegram channels.',
            detail: 'rpc error code 420: FLOOD_WAIT_30',
        });
        expect(authApi.createPersonalDrive).not.toHaveBeenCalled();
    });

    it('surfaces a failed saved-drive activation instead of a connection hint', async () => {
        authApi.preparePersonalDrive.mockRejectedValue(new Error('read config: permission denied'));

        await preparePersonalDriveAndContinue();

        expect(get(authScreen)).toBe('drive');
        expect(get(personalDriveSetup)).toMatchObject({
            phase: 'discovery-error',
            error: 'Could not open your saved drive.',
            detail: 'read config: permission denied',
        });
        expect(authApi.discoverPersonalDrives).not.toHaveBeenCalled();
    });

    it('ignores an older discovery response after a newer request finishes', async () => {
        const older = deferred<{ status: string; activeChannelId: string }>();
        const newer = deferred<{ status: string; activeChannelId: string }>();
        authApi.preparePersonalDrive
            .mockImplementationOnce(() => older.promise)
            .mockImplementationOnce(() => newer.promise);
        authApi.discoverPersonalDrives.mockResolvedValue([{
            id: '8300', title: 'Current choice', createdAt: 100,
            hasActivity: false, recommended: false,
        }]);

        const olderRequest = preparePersonalDriveAndContinue();
        await vi.waitFor(() => expect(authApi.preparePersonalDrive).toHaveBeenCalledOnce());
        const newerRequest = preparePersonalDriveAndContinue();

        newer.resolve({ status: 'selection_required', activeChannelId: '' });
        await newerRequest;

        older.resolve({ status: 'ready', activeChannelId: '8200' });
        await olderRequest;
        flushSync();

        expect(get(authScreen)).toBe('drive');
        expect(get(personalDriveSetup).candidates).toEqual([expect.objectContaining({ id: '8300' })]);
        expect(collaborators.loadChannels).not.toHaveBeenCalled();
    });

    it('enters the dashboard only after a selected channel recovers', async () => {
        personalDriveSetup.showCandidates([{
            id: '8200', title: 'TDrive', createdAt: 100,
            hasActivity: true, recommended: true,
        }]);
        showAuthView('drive');
        authApi.selectPersonalDrive.mockResolvedValue(undefined);

        await selectPersonalDrive('8200');

        expect(authApi.selectPersonalDrive).toHaveBeenCalledWith('8200');
        expect(get(appView)).toEqual({ kind: 'dashboard' });
    });

    it('keeps the picker active when selection fails', async () => {
        personalDriveSetup.showCandidates([{
            id: '8200', title: 'TDrive', createdAt: 100,
            hasActivity: true, recommended: true,
        }]);
        showAuthView('drive');
        authApi.selectPersonalDrive.mockRejectedValue(new Error('sync failed'));

        await selectPersonalDrive('8200');

        expect(get(authScreen)).toBe('drive');
        expect(get(personalDriveSetup).phase).toBe('ready');
        expect(get(personalDriveSetup).error).toContain('Could not recover');
        expect(get(personalDriveSetup).detail).toBe('sync failed');
    });

    it('creates only from the explicit create action', async () => {
        personalDriveSetup.showCandidates([]);
        showAuthView('drive');
        authApi.createPersonalDrive.mockResolvedValue(undefined);

        await createPersonalDrive();

        expect(authApi.createPersonalDrive).toHaveBeenCalledOnce();
        expect(get(appView)).toEqual({ kind: 'dashboard' });
    });

    it('offers an honest setup retry after creation does not finish', async () => {
        personalDriveSetup.showCandidates([]);
        showAuthView('drive');
        authApi.createPersonalDrive.mockRejectedValue(new Error('sync failed'));

        await createPersonalDrive();

        expect(get(personalDriveSetup)).toMatchObject({
            phase: 'ready',
            createRetry: true,
        });
        expect(get(personalDriveSetup).error).toContain('previous attempt');
    });

    it('does not publish identity from a superseded dashboard flow', async () => {
        const olderIdentity = deferred<number>();
        authApi.preparePersonalDrive.mockResolvedValue({ status: 'ready', activeChannelId: '8200' });
        authApi.getMyUserId.mockImplementationOnce(() => olderIdentity.promise);
        state.myUserID = 99;

        const olderRequest = preparePersonalDriveAndContinue();
        await vi.waitFor(() => expect(authApi.getMyUserId).toHaveBeenCalledOnce());
        expect(state.myUserID).toBe(0);

        await preparePersonalDriveAndContinue();
        expect(state.myUserID).toBe(77);
        olderIdentity.resolve(99);
        await olderRequest;

        expect(state.myUserID).toBe(77);
    });
});
