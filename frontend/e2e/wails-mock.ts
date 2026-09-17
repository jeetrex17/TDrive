import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test as base, type Page } from '@playwright/test';

type MockOutcome =
    | { kind: 'resolve'; value: unknown; delayMs: number }
    | { kind: 'reject'; message: string; delayMs: number }
    | { kind: 'return'; value: unknown };

export type MockPlan =
    | MockOutcome
    | { kind: 'galleryPage'; count: number; template: Record<string, unknown> }
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

/** Generate one page at the wire boundary so scale tests do not inject a full
 * 100k metadata array into the app under test. */
export function galleryPage(count: number, template: Record<string, unknown>): MockPlan {
    return { kind: 'galleryPage', count, template };
}

const MOCK_IMAGE_CAPABILITY = 'mock-original-image';

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
    GetMediaTimeline: resolves({ channel_id: 1, generation: 'test', total_count: 0, page_size: 128, buckets: [], anchors: [] }),
    OpenGalleryImages: resolves({ token: crypto.randomUUID(), base_url: '/mock-renditions', channel_id: 1 }),
    CloseGalleryImages: resolves(null),
    OpenOriginalImage: resolves({ token: MOCK_IMAGE_CAPABILITY, url: 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Y9ZcsQAAAAASUVORK5CYII=', thumbnail_url: '', hls_url: '', name: 'photo.png', kind: 'image', mime_type: 'image/png', supports_range: true, info: { channel_id: 1, file_id: 1, revision: 1, name: 'photo.png', stored_size: 68, plaintext_size: 68, encrypted: false, multipart: false } }),
    CloseMedia: resolves(null),
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

// The generated bindings (frontend/bindings/TDrive/app.ts) call
// `$Call.ByID(<numeric id>, ...args)` for every bound Go method — there is no
// name in the wire request. Recover the id -> method name mapping straight
// from that generated file instead of hand-copying 89 numbers, so this stays
// correct across regenerations.
const APP_BINDINGS_PATH = join(__dirname, '../bindings/TDrive/app.ts');

function loadMethodIdsByName(): Record<string, number> {
    const source = readFileSync(APP_BINDINGS_PATH, 'utf8');
    const ids: Record<string, number> = {};
    const functionPattern = /^export function (\w+)\(/gm;
    let match: RegExpExecArray | null;
    while ((match = functionPattern.exec(source)) !== null) {
        const name = match[1];
        const bodyStart = source.indexOf('{', match.index);
        const bodyEnd = source.indexOf('\n}', bodyStart);
        const body = source.slice(bodyStart, bodyEnd === -1 ? source.length : bodyEnd);
        const idMatch = /\$Call\.ByID\((\d+)/.exec(body);
        if (idMatch) ids[name] = Number(idMatch[1]);
    }
    return ids;
}

function methodNamesById(): Record<string, string> {
    const byName = loadMethodIdsByName();
    const byId: Record<string, string> = {};
    for (const [name, id] of Object.entries(byName)) byId[String(id)] = name;
    return byId;
}

export interface WailsMockHandle {
    calls(method?: string): Promise<MockCall[]>;
    emit(eventName: string, ...args: unknown[]): Promise<void>;
}

export async function bootTDrive(
    page: Page,
    methodOverrides: Record<string, MockPlan> = {},
): Promise<WailsMockHandle> {
    const methods = { ...DEFAULT_METHODS, ...methodOverrides };
    const methodNameById = methodNamesById();

    await page.addInitScript(({ configuredMethods, methodNameById: idToName }: {
        configuredMethods: Record<string, MockPlan>;
        methodNameById: Record<string, string>;
    }) => {
        const plans = configuredMethods;
        const calls: MockCall[] = [];

        const selectPlan = (candidate: MockPlan | undefined, args: unknown[]): MockOutcome => {
            const plan = candidate ?? { kind: 'resolve', value: null, delayMs: 0 };
            if (plan.kind === 'byFirstArg') {
                return selectPlan(plan.values[String(args[0])] ?? plan.fallback, args);
            }
            if (plan.kind === 'galleryPage') {
                const start = Number(args[0]);
                return { kind: 'resolve', delayMs: 0, value: {
                    generation: 'test', start_index: start, next_cursor: '',
                    items: Array.from({ length: Math.max(0, Math.min(128, plan.count - start)) }, (_, offset) => ({
                        ...plan.template, name: `photo-${start + offset}.jpg`, msg_id: 1000 + start + offset,
                        revision: 1, content_msg_id: 1000 + start + offset, content_hash: '',
                    })),
                } };
            }
            return plan;
        };

        // Wails v3 bound methods always go over the wire as a JSON POST to
        // /wails/runtime; there is no injected window.go/window.runtime
        // object anymore. Intercept that one endpoint instead.
        const RUNTIME_PATH = '/wails/runtime';
        const originalFetch = window.fetch.bind(window);

        const jsonResponse = (status: number, body: unknown): Response => new Response(JSON.stringify(body), {
            status,
            headers: { 'Content-Type': 'application/json' },
        });

        window.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
            const url = typeof input === 'string' || input instanceof URL ? input : input.url;
            const method = init?.method ?? (input instanceof Request ? input.method : 'GET');
            let pathname = '';
            try {
                pathname = new URL(url, window.location.origin).pathname;
            } catch {
                pathname = '';
            }
            if (pathname !== RUNTIME_PATH || method !== 'POST' || typeof init?.body !== 'string') {
                return originalFetch(input, init);
            }

            let body: { args?: Record<string, unknown> };
            try {
                body = JSON.parse(init.body);
            } catch {
                return originalFetch(input, init);
            }

            const callArgs = body.args;
            if (callArgs == null || typeof callArgs !== 'object' || !('call-id' in callArgs)) {
                // Window/System/Browser/Events/CancelCall calls the app makes
                // in the background (native theme sync, etc). None of these
                // e2e tests assert on them; a harmless empty success keeps
                // every caller's best-effort error handling quiet.
                return Promise.resolve(jsonResponse(200, {}));
            }

            const methodID = typeof callArgs.methodID === 'number' ? callArgs.methodID : undefined;
            const methodName = methodID !== undefined
                ? idToName[String(methodID)]
                : (typeof callArgs.methodName === 'string' ? callArgs.methodName : undefined);
            if (!methodName) return Promise.resolve(jsonResponse(200, {}));

            const args = Array.isArray(callArgs.args) ? callArgs.args : [];
            const plan = selectPlan(plans[methodName], args);
            const call: MockCall = { method: methodName, args, state: 'pending' };
            calls.push(call);

            if (plan.kind === 'return') {
                call.state = 'returned';
                return Promise.resolve(jsonResponse(200, plan.value));
            }

            if (plan.kind === 'reject') {
                const respond = () => {
                    call.state = 'rejected';
                    return jsonResponse(500, { message: plan.message, kind: 'RuntimeError' });
                };
                if (plan.delayMs === 0) return Promise.resolve(respond());
                return new Promise<Response>((resolve) => {
                    window.setTimeout(() => resolve(respond()), plan.delayMs);
                });
            }

            const respond = () => {
                call.state = 'fulfilled';
                return jsonResponse(200, plan.value);
            };
            if (plan.delayMs === 0) return Promise.resolve(respond());
            return new Promise<Response>((resolve) => {
                window.setTimeout(() => resolve(respond()), plan.delayMs);
            });
        }) as typeof window.fetch;

        // Go injects window._wails.environment (via an inline script before
        // the app bundle loads) in every real webview; its presence is what
        // the frontend's gateway-readiness check looks for.
        window._wails = window._wails || {};
        window._wails.environment = { OS: 'test', Arch: 'test', Debug: true };

        window.__wailsMock = {
            calls,
            emit(eventName: string, ...args: unknown[]) {
                // @wailsio/runtime's events module always wires up this hook
                // (window._wails.dispatchWailsEvent) once it loads — the same
                // entry point Go's native side uses to deliver real events.
                const wails = window._wails as unknown as {
                    dispatchWailsEvent?: (event: { name: string; data: unknown }) => void;
                };
                wails.dispatchWailsEvent?.({ name: eventName, data: args });
            },
        };
    }, { configuredMethods: methods, methodNameById });

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
