import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import Breadcrumb from './Breadcrumb.svelte';
import { breadcrumbPath, requestBreadcrumbEdit, type BreadcrumbDrag } from './breadcrumb-store';
import { openChannelKey } from '../channels/channel-store';
import { sidebarState } from '../sidebar/sidebar-store';

const noop = () => {};
const drag: BreadcrumbDrag = {
    isActive: () => false,
    canDrop: () => false,
    highlight: noop,
    leave: noop,
    dropOn: noop,
    registerRoot: noop,
};

let target: HTMLElement;
let component: ReturnType<typeof mount> | null = null;
const submit = vi.fn<(text: string, stillWanted: () => boolean) => Promise<string | null>>();
const navigate = vi.fn<(index: number) => void>();

function show(withSubmit = true, extra: Record<string, unknown> = {}) {
    component = mount(Breadcrumb, {
        target,
        props: { onNavigate: navigate, onBack: noop, drag, ...(withSubmit ? { onPathSubmit: submit } : {}), ...extra },
    });
    flushSync();
}

function paste(data: Record<string, string>) {
    const event = new Event('paste', { bubbles: true, cancelable: true });
    Object.defineProperty(event, 'clipboardData', { value: { getData: (type: string) => data[type] ?? '' } });
    field()!.dispatchEvent(event);
    flushSync();
    return event;
}

const field = () => document.getElementById('breadcrumb-path-input') as HTMLInputElement | null;

function press(el: EventTarget, init: KeyboardEventInit) {
    const event = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init });
    el.dispatchEvent(event);
    flushSync();
    return event;
}

function type(value: string) {
    const input = field()!;
    input.value = value;
    input.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
}

async function settle() {
    for (let i = 0; i < 3; i += 1) await Promise.resolve();
    await tick();
    flushSync();
}

function clickBesideCrumbs() {
    document.getElementById('breadcrumb-path')!.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    flushSync();
}

beforeEach(() => {
    submit.mockReset();
    submit.mockResolvedValue(null);
    navigate.mockReset();
    breadcrumbPath.set([{ id: 'd:docs', name: 'Docs' }, { id: 'd:taxes', name: 'Taxes' }]);
    sidebarState.set({
        personal: [{ id: 1, title: 'Personal', kind: 'personal' }],
        shared: [],
        pending: [],
        activeChannelId: 1,
        virtualView: null,
    } as never);
    target = document.createElement('div');
    document.body.append(target);
});

afterEach(() => {
    if (component) unmount(component);
    component = null;
    document.body.replaceChildren();
    breadcrumbPath.set([]);
    openChannelKey.set(null);
    sidebarState.set({ personal: [], shared: [], pending: [], activeChannelId: null, virtualView: null });
});

describe('Breadcrumb path field', () => {
    it('opens from the space beside the crumbs with the folder path selected', () => {
        show();
        clickBesideCrumbs();

        const input = field()!;
        expect(input.value).toBe('/Docs/Taxes');
        expect(document.activeElement).toBe(input);
        expect([input.selectionStart, input.selectionEnd]).toEqual([0, input.value.length]);
        // The trail keeps its place under the field, hidden from assistive tech.
        expect(document.getElementById('breadcrumb-path')?.getAttribute('aria-hidden')).toBe('true');
    });

    it('leaves crumb clicks to navigation', () => {
        show();
        (document.querySelector('.breadcrumb-link[data-index="0"]') as HTMLButtonElement).click();
        flushSync();
        expect(navigate).toHaveBeenCalledWith(0);
        expect(field()).toBeNull();
    });

    it('opens from the Edit path button and from a shortcut request', () => {
        show();
        document.getElementById('breadcrumb-edit')!.click();
        flushSync();
        expect(field()?.value).toBe('/Docs/Taxes');

        press(field()!, { key: 'Escape' });
        breadcrumbPath.set([]);
        requestBreadcrumbEdit();
        flushSync();
        expect(field()?.value).toBe('/');
    });

    it('goes to the typed path on Enter and closes once it opens', async () => {
        show();
        clickBesideCrumbs();
        type('/Team/Q4');
        expect(press(field()!, { key: 'Enter' }).defaultPrevented).toBe(true);
        await settle();

        expect(submit).toHaveBeenCalledWith('/Team/Q4', expect.any(Function));
        expect(field()).toBeNull();
        expect(document.getElementById('breadcrumb-path')?.hasAttribute('aria-hidden')).toBe(false);
    });

    it('keeps the text and says why when the path does not open', async () => {
        submit.mockResolvedValueOnce('"Q4" was not found in /Team.');
        show();
        clickBesideCrumbs();
        type('/Team/Q4');
        press(field()!, { key: 'Enter' });
        await settle();

        const input = field()!;
        const message = document.getElementById('breadcrumb-path-error')!;
        expect(message.textContent).toBe('"Q4" was not found in /Team.');
        expect(message.getAttribute('role')).toBe('alert');
        expect(input.value).toBe('/Team/Q4');
        expect(input.getAttribute('aria-invalid')).toBe('true');
        expect(input.getAttribute('aria-describedby')).toBe('breadcrumb-path-error');

        // The message takes focus only from a pointer, to select its text.
        expect(message.getAttribute('tabindex')).toBe('-1');

        type('/Team/Q');
        expect(document.getElementById('breadcrumb-path-error')).toBeNull();
        expect(field()!.hasAttribute('aria-invalid')).toBe(false);
    });

    it('stays open while focus moves to its message and closes when focus leaves', async () => {
        submit.mockResolvedValueOnce('"Q4" was not found in /Team.');
        const outside = document.createElement('button');
        document.body.append(outside);
        show();
        clickBesideCrumbs();
        press(field()!, { key: 'Enter' });
        await settle();
        const message = document.getElementById('breadcrumb-path-error')!;

        field()!.dispatchEvent(new FocusEvent('focusout', { bubbles: true, relatedTarget: message }));
        flushSync();
        expect(field()).not.toBeNull();

        message.dispatchEvent(new FocusEvent('focusout', { bubbles: true, relatedTarget: outside }));
        flushSync();
        expect(field()).toBeNull();
        expect(document.getElementById('breadcrumb-path')?.hasAttribute('aria-hidden')).toBe(false);
    });

    it('cancels on Escape from the message and leaves its other keys alone', async () => {
        submit.mockResolvedValueOnce('"Q4" was not found in /Team.');
        show();
        clickBesideCrumbs();
        press(field()!, { key: 'Enter' });
        await settle();
        const message = document.getElementById('breadcrumb-path-error')!;
        expect(message.closest('.breadcrumb-path-editor')).not.toBeNull();

        // F5 has to reach the app's refresh, which keeps the webview from reloading.
        expect(press(message, { key: 'F5' }).defaultPrevented).toBe(false);
        expect(field()).not.toBeNull();

        expect(press(message, { key: 'Escape' }).defaultPrevented).toBe(true);
        expect(field()).toBeNull();
    });

    it('selects the whole path again on Ctrl+L inside the field', () => {
        show();
        clickBesideCrumbs();
        const input = field()!;
        input.setSelectionRange(3, 3);
        expect(press(input, { key: 'l', ctrlKey: true }).defaultPrevented).toBe(true);
        expect([input.selectionStart, input.selectionEnd]).toEqual([0, input.value.length]);
        expect(press(input, { key: 'l', ctrlKey: true, shiftKey: true }).defaultPrevented).toBe(false);
    });

    it('ignores a paste while a path is opening', () => {
        submit.mockImplementationOnce(() => new Promise(() => {}));
        show();
        clickBesideCrumbs();
        press(field()!, { key: 'Enter' });
        expect(paste({ 'text/plain': '/Team' }).defaultPrevented).toBe(true);
        expect(field()!.value).toBe('/Docs/Taxes');
    });

    it('keeps the first line of a pasted list, and the first entry of a URI list', () => {
        show();
        clickBesideCrumbs();
        const input = field()!;

        input.setSelectionRange(0, input.value.length);
        expect(paste({ 'text/plain': '\n /Team/Q4 \nsecond line' }).defaultPrevented).toBe(true);
        expect(input.value).toBe('/Team/Q4');

        // Some Linux file managers copy a location only as text/uri-list.
        input.setSelectionRange(0, input.value.length);
        expect(paste({ 'text/uri-list': '# copied\r\nfile:///home/me/Docs\r\nfile:///home/me/More\r\n' }).defaultPrevented)
            .toBe(true);
        expect(input.value).toBe('file:///home/me/Docs');

        // Files (Nautilus) adds only the bare name as plain text; the file URI wins.
        input.setSelectionRange(0, input.value.length);
        expect(paste({ 'text/plain': 'Reports', 'text/uri-list': 'file:///home/me/Reports\r\n' }).defaultPrevented)
            .toBe(true);
        expect(input.value).toBe('file:///home/me/Reports');

        // A web link copied with its text keeps the field's own paste.
        expect(paste({ 'text/plain': '/Team', 'text/uri-list': 'https://example.com/' }).defaultPrevented)
            .toBe(false);

        // A single line is left to the field's own paste.
        expect(paste({ 'text/plain': '/Team' }).defaultPrevented).toBe(false);
        expect(paste({}).defaultPrevented).toBe(false);
    });

    it('tells the app each time the field opens', () => {
        const started = vi.fn();
        show(true, { onPathEditStart: started });
        clickBesideCrumbs();
        expect(started).toHaveBeenCalledTimes(1);
        // Already open: a shortcut request starts nothing new.
        requestBreadcrumbEdit();
        flushSync();
        expect(started).toHaveBeenCalledTimes(1);

        press(field()!, { key: 'Escape' });
        requestBreadcrumbEdit();
        flushSync();
        expect(started).toHaveBeenCalledTimes(2);
    });

    it('cancels with Escape and gives focus back to where it was', async () => {
        const outside = document.createElement('button');
        document.body.append(outside);
        show();
        outside.focus();
        requestBreadcrumbEdit();
        flushSync();
        expect(document.activeElement).toBe(field());

        expect(press(field()!, { key: 'Escape' }).defaultPrevented).toBe(true);
        await settle();
        expect(field()).toBeNull();
        expect(document.activeElement).toBe(outside);
        expect(submit).not.toHaveBeenCalled();
    });

    it('does not submit while an input method is composing', () => {
        show();
        clickBesideCrumbs();
        press(field()!, { key: 'Enter', isComposing: true });
        expect(submit).not.toHaveBeenCalled();
    });

    it('drops the answer for a path the reader walked away from', async () => {
        let answer: (message: string | null) => void = noop;
        submit.mockImplementationOnce(() => new Promise((resolve) => { answer = resolve; }));
        show();
        clickBesideCrumbs();
        press(field()!, { key: 'Enter' });
        const stillWanted = submit.mock.calls[0][1];
        expect(stillWanted()).toBe(true);
        expect(field()!.getAttribute('aria-busy')).toBe('true');

        press(field()!, { key: 'Escape' });
        expect(stillWanted()).toBe(false);
        // Open the field again: the late answer must not land in it.
        clickBesideCrumbs();
        answer('"Docs" was not found in Personal.');
        await settle();
        expect(field()).not.toBeNull();
        expect(field()!.hasAttribute('aria-busy')).toBe(false);
        expect(document.getElementById('breadcrumb-path-error')).toBeNull();
    });

    it('closes when Media or Trash takes the view', () => {
        show();
        clickBesideCrumbs();
        sidebarState.update((current) => ({ ...current, virtualView: 'trash' }) as never);
        flushSync();
        expect(field()).toBeNull();

        requestBreadcrumbEdit();
        flushSync();
        expect(field()).toBeNull();
    });

    it('closes when a Telegram channel covers the list, and stays shut while it is open', () => {
        show();
        clickBesideCrumbs();
        openChannelKey.set('channel');
        flushSync();
        expect(field()).toBeNull();

        requestBreadcrumbEdit();
        flushSync();
        expect(field()).toBeNull();
    });

    it('stays a plain trail without a submit handler', () => {
        show(false);
        clickBesideCrumbs();
        requestBreadcrumbEdit();
        flushSync();
        expect(field()).toBeNull();
        expect(document.getElementById('breadcrumb-edit')).toBeNull();
    });
});
