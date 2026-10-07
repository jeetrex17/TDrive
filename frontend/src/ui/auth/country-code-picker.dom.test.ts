import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import CountryCodePicker from './CountryCodePicker.svelte';
import { closeTopSheet } from '../modals/sheet-stack';

vi.mock('../../api', () => ({ isMobilePlatform: () => true }));

Object.defineProperty(HTMLElement.prototype, 'offsetParent', {
    configurable: true,
    get() { return this.parentElement; },
});

let host: HTMLFormElement;
let app: Record<string, unknown>;
const onSelect = vi.fn();

async function settle(): Promise<void> {
    flushSync();
    await tick();
    await Promise.resolve();
    flushSync();
}

function trigger(): HTMLButtonElement {
    return host.querySelector('#telegram-country') as HTMLButtonElement;
}

async function open(): Promise<void> {
    trigger().focus();
    trigger().click();
    await settle();
}

async function search(value: string): Promise<void> {
    const input = document.querySelector<HTMLInputElement>('#country-code-search')!;
    input.value = value;
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await settle();
}

function choices(): HTMLButtonElement[] {
    return Array.from(document.querySelectorAll<HTMLButtonElement>('#country-code-results button'));
}

beforeEach(() => {
    onSelect.mockReset();
    host = document.createElement('form');
    document.body.appendChild(host);
    app = mount(CountryCodePicker, { target: host, props: { country: 'IN', disabled: false, onSelect } });
    flushSync();
});

afterEach(async () => {
    await unmount(app);
    host.remove();
});

describe('country code picker', () => {
    it('names the selected country and opens a focused sheet outside the login form', async () => {
        expect(trigger().getAttribute('aria-label')).toBe('Country code: India +91');
        expect(trigger().textContent).toContain('+91');
        await open();
        const dialog = document.querySelector('[role="dialog"]')!;
        expect(dialog.classList.contains('modal-sheet')).toBe(true);
        expect(dialog.closest('form')).toBeNull();
        expect(document.activeElement?.id).toBe('country-code-search');
        expect(trigger().getAttribute('aria-expanded')).toBe('true');
    });

    it.each(['india', 'IN', '+91'])('finds a country using %s and restores focus after selection', async (query) => {
        await open();
        await search(query);
        const india = choices().find((button) => button.getAttribute('aria-label') === 'India +91')!;
        expect(india).toBeDefined();
        india.click();
        await settle();
        expect(onSelect).toHaveBeenCalledExactlyOnceWith('IN');
        expect(trigger().getAttribute('aria-expanded')).toBe('false');
        expect(document.activeElement).toBe(trigger());
    });

    it('shows countries sharing a calling code separately', async () => {
        await open();
        await search('+1');
        const labels = choices().map((button) => button.getAttribute('aria-label'));
        expect(labels).toContain('United States +1');
        expect(labels).toContain('Canada +1');
    });

    it('shows an empty result and never submits the surrounding form on Enter', async () => {
        const submit = vi.fn((event: Event) => event.preventDefault());
        host.addEventListener('submit', submit);
        await open();
        await search('not a country');
        expect(choices()).toHaveLength(0);
        expect(document.querySelector('[role="status"]')?.textContent).toContain('No countries found');
        const event = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true });
        document.querySelector('#country-code-search')!.dispatchEvent(event);
        expect(event.defaultPrevented).toBe(true);
        expect(submit).not.toHaveBeenCalled();
    });

    it('dismisses on Android Back without changing the selection and resets search on reopen', async () => {
        await open();
        await search('Canada');
        expect(closeTopSheet()).toBe(true);
        await settle();
        expect(trigger().getAttribute('aria-expanded')).toBe('false');
        expect(onSelect).not.toHaveBeenCalled();
        expect(document.activeElement).toBe(trigger());
        await open();
        expect(document.querySelector<HTMLInputElement>('#country-code-search')?.value).toBe('');
        expect(choices().length).toBeGreaterThan(200);
    });

    it('selects a single search result on Enter, but preserves an IME composition', async () => {
        await open();
        await search('Canada');
        const input = document.querySelector('#country-code-search')!;
        input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', isComposing: true, bubbles: true, cancelable: true }));
        await settle();
        expect(onSelect).not.toHaveBeenCalled();
        expect(trigger().getAttribute('aria-expanded')).toBe('true');
        input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }));
        await settle();
        expect(onSelect).toHaveBeenCalledExactlyOnceWith('CA');
    });

    it('dismisses on Escape and releases its Back claim when unmounted', async () => {
        await open();
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
        await settle();
        expect(trigger().getAttribute('aria-expanded')).toBe('false');
        expect(onSelect).not.toHaveBeenCalled();
        await open();
        await unmount(app);
        expect(closeTopSheet()).toBe(false);
        expect(document.getElementById('country-code-modal')).toBeNull();
        app = mount(CountryCodePicker, { target: host, props: { country: undefined, onSelect } });
        await settle();
        expect(trigger().getAttribute('aria-label')).toBe('Choose country code');
    });

    it('does not open while disabled', async () => {
        await unmount(app);
        app = mount(CountryCodePicker, { target: host, props: { country: undefined, disabled: true, onSelect } });
        await settle();
        expect(trigger().disabled).toBe(true);
        trigger().click();
        await settle();
        expect(document.querySelector('[role="dialog"]')).toBeNull();
    });
});
