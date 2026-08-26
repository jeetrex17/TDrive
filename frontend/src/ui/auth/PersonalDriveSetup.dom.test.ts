import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import PersonalDriveSetup from './PersonalDriveSetup.svelte';

let host: HTMLElement;
let app: Record<string, unknown>;
let onSelect: (channelID: string) => void;
let onCreate: () => void;
let onRetry: () => void;

const candidates = [{
    id: '8200',
    title: 'TDrive',
    created_at: 1_700_000_000,
    has_activity: true,
    recommended: true,
}, {
    id: '8300',
    title: 'Archive',
    created_at: 1_600_000_000,
    has_activity: false,
    recommended: false,
}];

beforeEach(() => {
    host = document.createElement('div');
    document.body.appendChild(host);
    onSelect = vi.fn<(channelID: string) => void>();
    onCreate = vi.fn<() => void>();
    onRetry = vi.fn<() => void>();
    app = mount(PersonalDriveSetup, {
        target: host,
        props: {
            phase: 'ready',
            candidates,
            error: '',
            onSelect,
            onCreate,
            onRetry,
        },
    });
});

afterEach(async () => {
    await unmount(app);
    host.remove();
});

describe('PersonalDriveSetup interactions', () => {
    it('requires an explicit radio selection before continuing', () => {
        const continueButton = host.querySelector<HTMLButtonElement>('[data-drive-continue]');
        expect(continueButton?.disabled).toBe(true);

        const choice = host.querySelector<HTMLInputElement>('input[value="8300"]');
        choice?.click();
        flushSync();
        expect(continueButton?.disabled).toBe(false);

        continueButton?.click();
        expect(onSelect).toHaveBeenCalledOnce();
        expect(onSelect).toHaveBeenCalledWith('8300');
    });

    it('requires confirmation before creating a new Telegram channel', () => {
        host.querySelector<HTMLButtonElement>('[data-drive-create-request]')?.click();
        flushSync();
        expect(onCreate).not.toHaveBeenCalled();
        expect(host.textContent).toContain('Create a new empty TDrive?');

        host.querySelector<HTMLButtonElement>('[data-drive-create-confirm]')?.click();
        expect(onCreate).toHaveBeenCalledOnce();
    });

    it('retries an interrupted create without repeating the new-channel copy', async () => {
        await unmount(app);
        app = mount(PersonalDriveSetup, {
            target: host,
            props: {
                phase: 'ready',
                candidates,
                error: 'The previous attempt did not finish.',
                createRetry: true,
                onSelect,
                onCreate,
                onRetry,
            },
        });
        flushSync();

        expect(host.textContent).toContain('Retry TDrive Setup');
        expect(host.textContent).toContain('without creating a duplicate channel');
        expect(host.textContent).not.toContain('This creates one new Telegram channel.');

        host.querySelector<HTMLButtonElement>('[data-drive-create-retry]')?.click();
        expect(onCreate).toHaveBeenCalledOnce();
        expect(host.querySelector('[data-drive-create-confirm]')).toBeNull();
    });

    it('disables every mutation control while recovery is running', async () => {
        await unmount(app);
        app = mount(PersonalDriveSetup, {
            target: host,
            props: {
                phase: 'recovering',
                candidates,
                error: '',
                onSelect,
                onCreate,
                onRetry,
            },
        });
        flushSync();

        const controls = [...host.querySelectorAll<HTMLButtonElement | HTMLInputElement>('button, input')];
        expect(controls.length).toBeGreaterThan(0);
        expect(controls.every((control) => control.disabled)).toBe(true);
        expect(host.querySelector('[role="status"]')?.textContent).toContain('Recovering');
    });

    it('retries discovery once from the error state', async () => {
        await unmount(app);
        app = mount(PersonalDriveSetup, {
            target: host,
            props: {
                phase: 'discovery-error',
                candidates: [],
                error: 'Offline',
                onSelect,
                onCreate,
                onRetry,
            },
        });
        host.querySelector<HTMLButtonElement>('[data-drive-retry]')?.click();
        expect(onRetry).toHaveBeenCalledOnce();
        expect(onCreate).not.toHaveBeenCalled();
    });
});
