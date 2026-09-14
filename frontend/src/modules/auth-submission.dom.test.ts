import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';
type RuntimeListener = (...args: unknown[]) => void;
type RuntimeDisconnector = ReturnType<typeof vi.fn<() => void>>;

const authApi = vi.hoisted(() => ({
    checkSystemStatus: vi.fn(),
    saveSetup: vi.fn(),
    loginPhoneNumber: vi.fn(),
    submitCode: vi.fn(),
    submitPassword: vi.fn(),
    checkLoginStatus: vi.fn(),
    preparePersonalDrive: vi.fn(),
    discoverPersonalDrives: vi.fn(),
    selectPersonalDrive: vi.fn(),
    createPersonalDrive: vi.fn(),
    getMyUserId: vi.fn(),
    syncChannel: vi.fn(),
    onRuntimeEvent: vi.fn(),
}));

const collaborators = vi.hoisted(() => ({
    loadChannels: vi.fn(),
    loadEncryptionStatus: vi.fn(),
    loadSelfUser: vi.fn(),
    renderBreadcrumb: vi.fn(),
    notify: vi.fn(),
}));

const harness = vi.hoisted(() => ({
    events: new Map<string, RuntimeListener>(),
    disconnectors: [] as RuntimeDisconnector[],
}));

vi.mock('../api', () => authApi);
vi.mock('./navigation', () => ({ renderBreadcrumb: collaborators.renderBreadcrumb }));
vi.mock('./channels', () => ({ loadChannels: collaborators.loadChannels }));
vi.mock('./encryption', () => ({ loadEncryptionStatus: collaborators.loadEncryptionStatus }));
vi.mock('./profile-menu', () => ({ loadSelfUser: collaborators.loadSelfUser }));
vi.mock('./notifications', () => ({
    notify: collaborators.notify,
    dismissNotification: vi.fn(),
}));

import { authScreenActions, connectAuthEvents } from './auth';
import { authScreen, showAuthView, showStartupView } from '../ui/app/app-store';
import { authSubmission, resetAuthSubmissions } from '../ui/auth/auth-store';
let disconnectAuthEvents: () => void = () => {};

function deferred<T>() {
    let resolve!: (value: T | PromiseLike<T>) => void;
    let reject!: (reason?: unknown) => void;
    const promise = new Promise<T>((promiseResolve, promiseReject) => {
        resolve = promiseResolve;
        reject = promiseReject;
    });
    return { promise, resolve, reject };
}

function props(): typeof authScreenActions {
    return authScreenActions;
}

function emit(name: string, ...args: unknown[]): void {
    const listener = harness.events.get(name);
    if (!listener) throw new Error(`runtime listener ${name} was not registered`);
    listener(...args);
}

beforeEach(() => {
    vi.clearAllMocks();
    harness.events.clear();
    harness.disconnectors.length = 0;
    authApi.onRuntimeEvent.mockImplementation((name: string, listener: RuntimeListener) => {
        harness.events.set(name, listener);
        const disconnect = vi.fn<() => void>(() => {
            if (harness.events.get(name) === listener) harness.events.delete(name);
        });
        harness.disconnectors.push(disconnect);
        return disconnect;
    });
    disconnectAuthEvents = connectAuthEvents();
    showAuthView('phone');
    resetAuthSubmissions();
});

afterEach(() => {
    disconnectAuthEvents();
    showStartupView();
    resetAuthSubmissions();
});

describe('auth submission lifecycle', () => {
    it('rejects a malformed API ID inline without calling the backend', async () => {
        showAuthView('setup');

        await props().onSetup('123abc', 'hash-value');

        expect(authApi.saveSetup).not.toHaveBeenCalled();
        expect(get(authSubmission).setup).toEqual({
            busy: false,
            error: 'Enter the numeric API ID from my.telegram.org/apps.',
        });
    });

    it('allows only one phone request in flight and humanizes its failure', async () => {
        const pending = deferred<void>();
        authApi.loginPhoneNumber.mockReturnValue(pending.promise);

        const first = props().onPhone(' +1 555 123 4567 ');
        const duplicate = props().onPhone('+1 555 123 4567');

        expect(authApi.loginPhoneNumber).toHaveBeenCalledOnce();
        expect(authApi.loginPhoneNumber).toHaveBeenCalledWith('+1 555 123 4567');
        expect(get(authSubmission).phone.busy).toBe(true);

        pending.reject(new Error('network connection failed'));
        await Promise.all([first, duplicate]);

        expect(get(authSubmission).phone).toEqual({
            busy: false,
            error: 'Telegram is not reachable right now. Try again.',
        });
        expect(get(authScreen)).toBe('phone');
    });

    it('keeps code submission busy until Telegram accepts or rejects it', async () => {
        showAuthView('code');
        authApi.submitCode.mockResolvedValue(undefined);

        await props().onCode(' 12345 ');

        expect(authApi.submitCode).toHaveBeenCalledWith('12345');
        expect(get(authSubmission).code).toEqual({ busy: true, error: '' });

        emit('login-code-invalid');

        expect(get(authScreen)).toBe('code');
        expect(get(authSubmission).code).toEqual({
            busy: false,
            error: 'That code was incorrect. Check it and try again.',
        });
        expect(collaborators.notify).not.toHaveBeenCalled();
    });

    it('moves code busy state to the password flow and surfaces terminal errors inline', async () => {
        showAuthView('code');
        authApi.submitCode.mockResolvedValue(undefined);
        authApi.submitPassword.mockResolvedValue(undefined);
        await props().onCode('12345');

        emit('login-password-required');
        expect(get(authScreen)).toBe('password');
        expect(get(authSubmission).code.busy).toBe(false);

        await props().onPassword('correct horse battery staple');
        expect(get(authSubmission).password.busy).toBe(true);

        emit('login-error', 'telegram network connection failed');
        expect(get(authSubmission).password).toEqual({
            busy: false,
            error: 'Telegram is not reachable right now. Try again.',
        });
        expect(collaborators.notify).not.toHaveBeenCalled();
    });
    it('tears down every auth event subscription exactly once', () => {
        expect(harness.events.size).toBe(6);

        disconnectAuthEvents();
        disconnectAuthEvents();

        expect(harness.events.size).toBe(0);
        for (const disconnect of harness.disconnectors) {
            expect(disconnect).toHaveBeenCalledOnce();
        }
    });

});
