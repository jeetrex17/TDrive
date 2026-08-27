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

    it('submits the highlighted choice on Enter', () => {
        const choice = host.querySelector<HTMLInputElement>('input[value="8200"]');
        choice?.click();
        flushSync();

        choice?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
        expect(onSelect).toHaveBeenCalledOnce();
        expect(onSelect).toHaveBeenCalledWith('8200');

        choice?.dispatchEvent(new KeyboardEvent('keydown', { key: ' ', bubbles: true }));
        expect(onSelect).toHaveBeenCalledOnce();
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
        expect(host.querySelector<HTMLButtonElement>('[data-drive-continue]')?.textContent).toContain('Recovering');
        // The announced status is the live scan line, not a static message.
        expect(host.querySelector('[data-drive-scan] [role="status"]')?.textContent).toContain('Reading your Telegram channel');
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

describe('PersonalDriveSetup recovery progress', () => {
    function remount(props: Record<string, unknown>): void {
        void unmount(app);
        app = mount(PersonalDriveSetup, {
            target: host,
            props: { phase: 'recovering', candidates, error: '', onSelect, onCreate, onRetry, ...props },
        });
        flushSync();
    }

    const track = () => host.querySelector<HTMLElement>('[role="progressbar"]');
    const fill = () => host.querySelector<HTMLElement>('.drive-scan-fill');
    const label = () => host.querySelector<HTMLElement>('[data-drive-scan] .drive-hint')?.textContent?.trim();

    const scan = (over: Record<string, unknown> = {}) => ({
        phase: 'applying', pages_done: 3, pages_total: 12,
        messages_done: 300, messages_total: 1200, wait_seconds: 0, ...over,
    });

    it('shows no progress block outside recovery', () => {
        remount({ phase: 'ready' });
        expect(host.querySelector('[data-drive-scan]')).toBeNull();
    });

    it('stays indeterminate until a total is known', () => {
        remount({ scan: null });
        expect(fill()?.classList.contains('indeterminate')).toBe(true);
        expect(track()?.getAttribute('aria-valuenow')).toBeNull();
        expect(label()).toContain('Reading your Telegram channel');

        remount({ scan: scan({ phase: 'counting', messages_done: 1500, messages_total: 0 }) });
        expect(fill()?.classList.contains('indeterminate')).toBe(true);
        expect(label()).toContain('Counting messages');
        expect(label()).toContain((1500).toLocaleString());
    });

    it('fills proportionally once the apply pass reports a total', () => {
        remount({ scan: scan() });
        expect(track()?.getAttribute('aria-valuenow')).toBe('25');
        expect(fill()?.style.width).toBe('25%');
        expect(fill()?.classList.contains('indeterminate')).toBe(false);
        expect(label()).toContain(`${(300).toLocaleString()} of ${(1200).toLocaleString()} messages`);

        remount({ scan: scan({ messages_done: 1200 }) });
        expect(track()?.getAttribute('aria-valuenow')).toBe('100');
    });

    it('counts a rate-limit pause down without losing the bar position', () => {
        vi.useFakeTimers();
        try {
            remount({ scan: scan(), waitSeconds: 30 });
            expect(label()).toContain('resuming in 30s');
            expect(fill()?.style.width).toBe('25%');

            vi.advanceTimersByTime(2000);
            flushSync();
            expect(label()).toContain('resuming in 28s');

            vi.advanceTimersByTime(60_000);
            flushSync();
            expect(label()).not.toContain('resuming in');
            expect(label()).toContain('Rebuilding your files');
        } finally {
            vi.useRealTimers();
        }
    });
});
