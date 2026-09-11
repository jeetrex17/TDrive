import { afterAll, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';

interface AuthProps {
    onSetup: (apiId: string, apiHash: string) => Promise<void>;
    onPhone: (phone: string) => Promise<void>;
    onCode: (code: string) => Promise<void>;
    onPassword: (password: string) => Promise<void>;
    onBackToPhone: () => void;
}

type RuntimeListener = (...args: unknown[]) => void;

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
    props: null as AuthProps | null,
    events: new Map<string, RuntimeListener>(),
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
vi.mock('../ui/mount', () => ({
    mountSvelte: vi.fn((_component: unknown, options: { props: AuthProps }) => {
        harness.props = options.props;
        return { destroy: vi.fn() };
    }),
}));

import { setupAuthWindowBindings } from './auth';
import {
    authScreen,
    authSubmission,
    resetAuthSubmissions,
} from '../ui/auth/auth-store';

function deferred<T>() {
    let resolve!: (value: T | PromiseLike<T>) => void;
    let reject!: (reason?: unknown) => void;
    const promise = new Promise<T>((promiseResolve, promiseReject) => {
        resolve = promiseResolve;
        reject = promiseReject;
    });
    return { promise, resolve, reject };
}

function props(): AuthProps {
    if (!harness.props) throw new Error('auth callbacks were not mounted');
    return harness.props;
}

function emit(name: string, ...args: unknown[]): void {
    const listener = harness.events.get(name);
    if (!listener) throw new Error(`runtime listener ${name} was not registered`);
    listener(...args);
}

beforeAll(() => {
    document.body.innerHTML = '<div id="auth-wrapper"></div><div id="success-screen"></div>';
    authApi.onRuntimeEvent.mockImplementation((name: string, listener: RuntimeListener) => {
        harness.events.set(name, listener);
        return () => undefined;
    });
    setupAuthWindowBindings();
});

beforeEach(() => {
    vi.clearAllMocks();
    authScreen.set('phone');
    resetAuthSubmissions();
});

afterAll(() => {
    authScreen.set(null);
    resetAuthSubmissions();
    document.body.innerHTML = '';
});

describe('auth submission lifecycle', () => {
    it('rejects a malformed API ID inline without calling the backend', async () => {
        authScreen.set('setup');

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
        authScreen.set('code');
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
        authScreen.set('code');
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
});
