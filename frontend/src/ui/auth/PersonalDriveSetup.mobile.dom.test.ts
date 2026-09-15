// Phone layout of the drive picker: actions move into the bottom bar and the
// discovery Retry guards itself against a second tap.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import PersonalDriveSetup from './PersonalDriveSetup.svelte';

let host: HTMLElement;
let app: Record<string, unknown> | null = null;

const candidates = [{
    id: '8200', title: 'TDrive', createdAt: 1_700_000_000, hasActivity: true, recommended: true,
}];

function setup(props: Record<string, unknown>): void {
    app = mount(PersonalDriveSetup, {
        target: host,
        props: { phase: 'ready', candidates, error: '', onSelect: vi.fn(), onCreate: vi.fn(), onRetry: vi.fn(), ...props },
    });
    flushSync();
}

beforeEach(() => {
    window.history.replaceState(null, '', '/?mobile=1');
    host = document.createElement('div');
    document.body.appendChild(host);
});

afterEach(async () => {
    if (app) await unmount(app);
    app = null;
    host.remove();
    window.history.replaceState(null, '', '/');
});

describe('PersonalDriveSetup on a phone', () => {
    it('pins the primary and create actions under the list', () => {
        setup({});
        const actions = host.querySelector('.auth-actions');
        expect(actions).not.toBeNull();
        expect(actions?.querySelector('[data-drive-continue]')?.textContent).toContain('Use this drive');
        expect(actions?.querySelector('[data-drive-create-request]')).not.toBeNull();
        expect(host.querySelector('.auth-page-body [data-drive-continue]')).toBeNull();
        expect(host.querySelector('.auth-page-body [data-drive-create-request]')).toBeNull();
    });

    it('promotes create to the primary action when the account has no channels', () => {
        setup({ candidates: [] });
        const create = host.querySelector<HTMLButtonElement>('[data-drive-create-request]');
        expect(create?.classList.contains('is-primary')).toBe(true);
        expect(host.querySelector('[data-drive-continue]')).toBeNull();
    });

    it('guards discovery retry against a double tap until it settles', async () => {
        let settle!: () => void;
        const onRetry = vi.fn(() => new Promise<void>((resolve) => { settle = resolve; }));
        setup({ phase: 'discovery-error', candidates: [], error: 'Offline', onRetry });

        const retry = host.querySelector<HTMLButtonElement>('.auth-actions [data-drive-retry]');
        if (!retry) throw new Error('retry not rendered in the action bar');
        retry.click();
        flushSync();
        retry.click();

        expect(onRetry).toHaveBeenCalledOnce();
        expect(retry.disabled).toBe(true);
        expect(retry.textContent).toContain('Retrying');

        settle();
        await vi.waitFor(() => expect(retry.disabled).toBe(false));
        expect(retry.textContent).toContain('Retry');
        expect(retry.textContent).not.toContain('Retrying');
    });
});
