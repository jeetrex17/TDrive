import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import AppRoot from './AppRoot.svelte';
import { showStartupView, type AppLifecycle } from './app-store';

let host: HTMLElement;
let app: Record<string, unknown> | null = null;

function mountRoot(lifecycle: AppLifecycle): void {
    app = mount(AppRoot, { target: host, props: { lifecycle } });
    flushSync();
}

beforeEach(() => {
    showStartupView();
    host = document.createElement('div');
    document.body.appendChild(host);
});

afterEach(async () => {
    if (app) await unmount(app);
    app = null;
    host.remove();
    showStartupView();
    vi.restoreAllMocks();
});

describe('persistent application root', () => {
    it('starts once and owns lifecycle cleanup when the root is destroyed', async () => {
        const lifecycle: AppLifecycle = {
            start: vi.fn(() => new Promise<void>(() => undefined)),
            stop: vi.fn(),
        };

        mountRoot(lifecycle);
        expect(lifecycle.start).toHaveBeenCalledOnce();
        expect(host.textContent).toContain('Starting TDrive');

        if (app) await unmount(app);
        app = null;
        expect(lifecycle.stop).toHaveBeenCalledOnce();
    });

    it('normalizes a boot failure into the fatal root view and cleans up once', async () => {
        const lifecycle: AppLifecycle = {
            start: vi.fn(async () => { throw new Error('native bridge failed'); }),
            stop: vi.fn(),
        };
        vi.spyOn(console, 'error').mockImplementation(() => undefined);

        mountRoot(lifecycle);
        await vi.waitFor(() => {
            expect(host.textContent).toContain('TDrive could not start');
            expect(host.textContent).toContain('Reload TDrive');
        });
        expect(lifecycle.stop).toHaveBeenCalledOnce();

        if (app) await unmount(app);
        app = null;
        expect(lifecycle.stop).toHaveBeenCalledOnce();
    });
});
