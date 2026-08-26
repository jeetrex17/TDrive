import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync } from 'svelte';
import { get } from 'svelte/store';

const bindings = vi.hoisted(() => ({
    CheckSystemStatus: vi.fn(),
    SaveSetup: vi.fn(),
    LoginPhoneNumber: vi.fn(),
    SumbitCode: vi.fn(),
    SumbitPassword: vi.fn(),
    CheckLoginStatus: vi.fn(),
    PreparePersonalDrive: vi.fn(),
    SelectPersonalDrive: vi.fn(),
    CreatePersonalDrive: vi.fn(),
    MyUserID: vi.fn(),
    SyncChannel: vi.fn(),
}));

const collaborators = vi.hoisted(() => ({
    loadChannels: vi.fn(),
    loadEncryptionStatus: vi.fn(),
    loadSelfUser: vi.fn(),
    renderBreadcrumb: vi.fn(),
    notify: vi.fn(),
    dismissNotification: vi.fn(),
}));

vi.mock('../../wailsjs/go/main/App', () => bindings);
vi.mock('./navigation', () => ({ renderBreadcrumb: collaborators.renderBreadcrumb }));
vi.mock('./channels', () => ({ loadChannels: collaborators.loadChannels }));
vi.mock('./encryption', () => ({ loadEncryptionStatus: collaborators.loadEncryptionStatus }));
vi.mock('./profile-menu', () => ({ loadSelfUser: collaborators.loadSelfUser }));
vi.mock('./notifications', () => ({
    notify: collaborators.notify,
    dismissNotification: collaborators.dismissNotification,
}));
vi.mock('../ui/mount', () => ({ mountSvelte: vi.fn(() => ({ destroy: vi.fn() })) }));

import {
    createPersonalDrive,
    preparePersonalDriveAndContinue,
    selectPersonalDrive,
} from './auth';
import { authScreen } from '../ui/auth/auth-store';
import { personalDriveSetup } from '../ui/auth/personal-drive-store';

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
    vi.clearAllMocks();
    document.body.innerHTML = `
        <div id="auth-wrapper" style="display: flex"></div>
        <div id="success-screen" style="display: none"></div>
    `;
    Object.defineProperty(window, 'triggerRefresh', {
        configurable: true,
        value: vi.fn(async () => undefined),
    });
    bindings.MyUserID.mockResolvedValue(77);
    bindings.SyncChannel.mockResolvedValue(undefined);
    collaborators.loadChannels.mockResolvedValue(undefined);
    collaborators.loadEncryptionStatus.mockResolvedValue(undefined);
    authScreen.set(null);
    personalDriveSetup.reset();
});

afterEach(() => {
    document.body.innerHTML = '';
});

describe('personal drive startup gate', () => {
    it('takes the saved-config fast path directly to the dashboard', async () => {
        bindings.PreparePersonalDrive.mockResolvedValue({
            status: 'ready',
            active_channel_id: '8200',
            candidates: [],
        });

        await preparePersonalDriveAndContinue();

        expect(bindings.PreparePersonalDrive).toHaveBeenCalledOnce();
        expect(get(authScreen)).toBeNull();
        expect(document.querySelector<HTMLElement>('#success-screen')?.style.display).toBe('flex');
        expect(collaborators.loadChannels).toHaveBeenCalledOnce();
    });

    it('keeps the dashboard hidden and shows candidates when selection is required', async () => {
        bindings.PreparePersonalDrive.mockResolvedValue({
            status: 'selection_required',
            active_channel_id: '',
            candidates: [{
                id: '8200', title: 'TDrive', created_at: 100,
                has_activity: true, recommended: true,
            }],
        });

        await preparePersonalDriveAndContinue();
        flushSync();

        expect(get(authScreen)).toBe('drive');
        expect(get(personalDriveSetup).candidates).toHaveLength(1);
        expect(document.querySelector<HTMLElement>('#success-screen')?.style.display).toBe('none');
        expect(bindings.CreatePersonalDrive).not.toHaveBeenCalled();
        expect(collaborators.loadChannels).not.toHaveBeenCalled();
    });

    it('surfaces discovery failure with Retry and never creates', async () => {
        bindings.PreparePersonalDrive.mockRejectedValue(new Error('offline'));

        await preparePersonalDriveAndContinue();

        expect(get(authScreen)).toBe('drive');
        expect(get(personalDriveSetup)).toMatchObject({
            phase: 'discovery-error',
            error: 'Could not look up your Telegram channels. Check your connection and try again.',
        });
        expect(bindings.CreatePersonalDrive).not.toHaveBeenCalled();
        expect(document.querySelector<HTMLElement>('#success-screen')?.style.display).toBe('none');
    });

    it('ignores an older discovery response after a newer request finishes', async () => {
        const older = deferred<{ status: string; active_channel_id: string; candidates: unknown[] }>();
        const newer = deferred<{ status: string; active_channel_id: string; candidates: unknown[] }>();
        bindings.PreparePersonalDrive
            .mockImplementationOnce(() => older.promise)
            .mockImplementationOnce(() => newer.promise);

        const olderRequest = preparePersonalDriveAndContinue();
        const newerRequest = preparePersonalDriveAndContinue();

        newer.resolve({
            status: 'selection_required',
            active_channel_id: '',
            candidates: [{
                id: '8300', title: 'Current choice', created_at: 100,
                has_activity: false, recommended: false,
            }],
        });
        await newerRequest;

        older.resolve({ status: 'ready', active_channel_id: '8200', candidates: [] });
        await olderRequest;
        flushSync();

        expect(get(authScreen)).toBe('drive');
        expect(get(personalDriveSetup).candidates).toEqual([expect.objectContaining({ id: '8300' })]);
        expect(document.querySelector<HTMLElement>('#success-screen')?.style.display).toBe('none');
        expect(collaborators.loadChannels).not.toHaveBeenCalled();
    });

    it('enters the dashboard only after a selected channel recovers', async () => {
        personalDriveSetup.showCandidates([{
            id: '8200', title: 'TDrive', created_at: 100,
            has_activity: true, recommended: true,
        }]);
        authScreen.set('drive');
        bindings.SelectPersonalDrive.mockResolvedValue(undefined);

        await selectPersonalDrive('8200');

        expect(bindings.SelectPersonalDrive).toHaveBeenCalledWith('8200');
        expect(document.querySelector<HTMLElement>('#success-screen')?.style.display).toBe('flex');
    });

    it('keeps the picker active when selection fails', async () => {
        personalDriveSetup.showCandidates([{
            id: '8200', title: 'TDrive', created_at: 100,
            has_activity: true, recommended: true,
        }]);
        authScreen.set('drive');
        bindings.SelectPersonalDrive.mockRejectedValue(new Error('sync failed'));

        await selectPersonalDrive('8200');

        expect(get(authScreen)).toBe('drive');
        expect(get(personalDriveSetup).phase).toBe('ready');
        expect(get(personalDriveSetup).error).toContain('Could not recover');
        expect(document.querySelector<HTMLElement>('#success-screen')?.style.display).toBe('none');
    });

    it('creates only from the explicit create action', async () => {
        personalDriveSetup.showCandidates([]);
        authScreen.set('drive');
        bindings.CreatePersonalDrive.mockResolvedValue(undefined);

        await createPersonalDrive();

        expect(bindings.CreatePersonalDrive).toHaveBeenCalledOnce();
        expect(document.querySelector<HTMLElement>('#success-screen')?.style.display).toBe('flex');
    });

    it('offers an honest setup retry after creation does not finish', async () => {
        personalDriveSetup.showCandidates([]);
        authScreen.set('drive');
        bindings.CreatePersonalDrive.mockRejectedValue(new Error('sync failed'));

        await createPersonalDrive();

        expect(get(personalDriveSetup)).toMatchObject({
            phase: 'ready',
            createRetry: true,
        });
        expect(get(personalDriveSetup).error).toContain('previous attempt');
        expect(document.querySelector<HTMLElement>('#success-screen')?.style.display).toBe('none');
    });
});
