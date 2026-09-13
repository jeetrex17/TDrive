import { expect, test as base, type Page } from '@playwright/test';

type MockOutcome =
    | { kind: 'resolve'; value: unknown; delayMs: number }
    | { kind: 'reject'; message: string; delayMs: number }
    | { kind: 'return'; value: unknown };

type PromiseWithResolvers = typeof Promise & {
    withResolvers<T>(): {
        promise: Promise<T>;
        resolve: (value: T | PromiseLike<T>) => void;
        reject: (reason?: unknown) => void;
    };
};

export type MockPlan =
    | MockOutcome
    | { kind: 'byFirstArg'; values: Record<string, MockPlan>; fallback: MockPlan };

export interface MockCall {
    method: string;
    args: unknown[];
    state: 'pending' | 'fulfilled' | 'rejected' | 'returned';
}

interface BrowserMock {
    calls: MockCall[];
    emit: (eventName: string, ...args: unknown[]) => void;
}

declare global {
    interface Window {
        __wailsMock: BrowserMock;
    }
}

export function resolves(value: unknown, delayMs = 0): MockPlan {
    return { kind: 'resolve', value, delayMs };
}

export function rejects(message: string, delayMs = 0): MockPlan {
    return { kind: 'reject', message, delayMs };
}

export function returnsSynchronously(value: unknown): MockPlan {
    return { kind: 'return', value };
}

export function byFirstArg(values: Record<string, MockPlan>, fallback: MockPlan = resolves(null)): MockPlan {
    return { kind: 'byFirstArg', values, fallback };
}

const DEFAULT_METHODS: Record<string, MockPlan> = {
    AppVersion: resolves({ version: '0.0.0-test', os: 'test', arch: 'test' }),
    CheckForUpdate: resolves({ phase: 'up_to_date', current_version: '0.0.0-test' }),
    CheckLoginStatus: resolves(true),
    CheckSystemStatus: resolves('READY'),
    EncryptionStatus: resolves({
        available: true,
        password_set: false,
        password_remembered: false,
        hint: '',
    }),
    GetAllFsMsgIDs: resolves([]),
    GetFileList: resolves([]),
    GetFolderContents: resolves({ folders: [], files: [] }),
    GetStorageUsed: resolves(0),
    GetUpdateState: resolves({ phase: 'idle', current_version: '0.0.0-test' }),
    ListChannels: resolves([
        {
            id: 1,
            title: 'Personal',
            kind: 'personal',
            is_active: true,
            invite_link: '',
        },
    ]),
    ListMedia: resolves([]),
    ListPendingJoins: resolves([]),
    Me: resolves({ user_id: 7, display_name: 'Test User', username: 'test', photo_base64: '' }),
    MountDrive: resolves({
        result: { ok: true },
        mount: { mounted: true, phase: 'mounted', label: 'Tdrive personal' },
    }),
    MountDrives: resolves({
        result: { ok: true },
        mount: { mounted: true, phase: 'mounted', label: 'Tdrive' },
    }),
    MountStatus: resolves({ mounted: false, phase: 'idle', label: 'Tdrive personal' }),
    MyUserID: resolves(7),
    PreparePersonalDrive: resolves({ status: 'ready', active_channel_id: '1' }),
    ResolveUsernames: resolves({}),
    SetFileDropEnabled: resolves(null),
    SyncChannel: resolves(null),
};

export interface WailsMockHandle {
    calls(method?: string): Promise<MockCall[]>;
    emit(eventName: string, ...args: unknown[]): Promise<void>;
}

export async function bootTDrive(
    page: Page,
    methodOverrides: Record<string, MockPlan> = {},
): Promise<WailsMockHandle> {
    const methods = { ...DEFAULT_METHODS, ...methodOverrides };

    await page.addInitScript((configuredMethods: Record<string, MockPlan>) => {
        type EventListener = (...args: unknown[]) => void;

        const plans = configuredMethods;
        const calls: MockCall[] = [];
        const listeners = new Map<string, Set<EventListener>>();

        const selectPlan = (candidate: MockPlan | undefined, args: unknown[]): MockOutcome => {
            const plan = candidate ?? { kind: 'resolve', value: null, delayMs: 0 };
            if (plan.kind === 'byFirstArg') {
                return selectPlan(plan.values[String(args[0])] ?? plan.fallback, args);
            }
            return plan;
        };

        const invoke = (method: string, args: unknown[]): unknown => {
            const plan = selectPlan(plans[method], args);
            const call: MockCall = { method, args, state: 'pending' };
            calls.push(call);

            if (plan.kind === 'return') {
                call.state = 'returned';
                return plan.value;
            }

            if (plan.kind === 'reject') {
                if (plan.delayMs === 0) {
                    call.state = 'rejected';
                    return Promise.reject(new Error(plan.message));
                }
                const deferred = (Promise as PromiseWithResolvers).withResolvers<unknown>();
                window.setTimeout(() => {
                    call.state = 'rejected';
                    deferred.reject(new Error(plan.message));
                }, plan.delayMs);
                return deferred.promise;
            }

            if (plan.delayMs === 0) {
                call.state = 'fulfilled';
                return Promise.resolve(plan.value);
            }
            const deferred = (Promise as PromiseWithResolvers).withResolvers<unknown>();
            window.setTimeout(() => {
                call.state = 'fulfilled';
                deferred.resolve(plan.value);
            }, plan.delayMs);
            return deferred.promise;
        };

        const subscribe = (eventName: string, callback: EventListener): (() => void) => {
            const eventListeners = listeners.get(eventName) ?? new Set<EventListener>();
            eventListeners.add(callback);
            listeners.set(eventName, eventListeners);
            return () => {
                eventListeners.delete(callback);
                if (eventListeners.size === 0) listeners.delete(eventName);
            };
        };

        const runtimeMethods = {
            BrowserOpenURL: () => undefined,
            EventsEmit: (eventName: string, ...args: unknown[]) => {
                for (const callback of [...(listeners.get(eventName) ?? [])]) callback(...args);
            },
            EventsOff: (eventName: string) => listeners.delete(eventName),
            EventsOn: (eventName: string, callback: EventListener) => subscribe(eventName, callback),
            EventsOnMultiple: (eventName: string, callback: EventListener, maxCallbacks: number) => {
                let remaining = maxCallbacks;
                let unsubscribe: () => void = () => undefined;
                const limited: EventListener = (...args) => {
                    callback(...args);
                    if (remaining > 0 && --remaining === 0) unsubscribe();
                };
                unsubscribe = subscribe(eventName, limited);
                return unsubscribe;
            },
            EventsOnce: (eventName: string, callback: EventListener) => {
                let unsubscribe: () => void = () => undefined;
                const once: EventListener = (...args) => {
                    unsubscribe();
                    callback(...args);
                };
                unsubscribe = subscribe(eventName, once);
                return unsubscribe;
            },
            OnFileDrop: () => undefined,
            OnFileDropOff: () => undefined,
            WindowFullscreen: () => undefined,
            WindowIsFullscreen: () => Promise.resolve(false),
            WindowSetDarkTheme: () => undefined,
            WindowSetLightTheme: () => undefined,
            WindowSetSystemDefaultTheme: () => undefined,
            WindowUnfullscreen: () => undefined,
        };

        const app = new Proxy<Record<string, (...args: unknown[]) => unknown>>({}, {
            get: (_target, property) => {
                if (typeof property !== 'string') return undefined;
                return (...args: unknown[]) => invoke(property, args);
            },
        });
        const runtime = new Proxy(runtimeMethods as Record<string, unknown>, {
            get: (target, property) => {
                if (typeof property !== 'string') return undefined;
                return property in target ? target[property] : () => undefined;
            },
        });

        Object.defineProperty(window, 'go', {
            configurable: true,
            value: { main: { App: app } },
        });
        Object.defineProperty(window, 'runtime', {
            configurable: true,
            value: runtime,
        });
        window.__wailsMock = {
            calls,
            emit(eventName, ...args) {
                for (const callback of [...(listeners.get(eventName) ?? [])]) callback(...args);
            },
        };
    }, methods);

    await page.goto('/');

    return {
        calls: (method?: string) => page.evaluate((name) => {
            const calls = window.__wailsMock.calls;
            return name ? calls.filter((call) => call.method === name) : calls;
        }, method),
        emit: (eventName: string, ...args: unknown[]) => page.evaluate(
            ([name, eventArgs]) => window.__wailsMock.emit(name, ...eventArgs),
            [eventName, args] as const,
        ),
    };
}

type HarnessFixtures = {
    pageErrors: string[];
};

export const test = base.extend<HarnessFixtures>({
    pageErrors: [async ({ page }, use) => {
        const errors: string[] = [];
        const record = (error: Error) => errors.push(error.stack ?? error.message);
        page.on('pageerror', record);
        await use(errors);
        page.off('pageerror', record);
        expect(errors, 'uncaught browser errors').toEqual([]);
    }, { auto: true }],
});

export { expect };
