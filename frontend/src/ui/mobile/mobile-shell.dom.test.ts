// The top bar's divider: it marks content passing underneath, so it has to be
// read from the surface that is actually showing.
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import MobileShell from './MobileShell.svelte';
import { sidebarState } from '../sidebar/sidebar-store';
import { activeTab } from './mobile-shell-store';
import { selectionBarState } from '../selection/selection-bar-store';
import { fileListView } from '../file-list/file-list-store';

let target: HTMLElement;
let component: Record<string, unknown> | null = null;

function shell(): HTMLElement {
    return target.querySelector('.mobile-shell') as HTMLElement;
}

function scrollTo(selector: string, top: number): void {
    const el = target.querySelector(selector) as HTMLElement;
    Object.defineProperty(el, 'scrollTop', { value: top, configurable: true });
    el.dispatchEvent(new Event('scroll'));
    flushSync();
}

beforeEach(() => {
    activeTab.set('files');
    selectionBarState.set({ count: 0 });
    fileListView.set({ kind: 'state', stateKind: 'loading', title: 'Loading files' });
    sidebarState.update((current) => ({ ...current, virtualView: null }));
    target = document.createElement('div');
    document.body.append(target);
    component = mount(MobileShell, { target, props: { dashboardVisible: true } });
    flushSync();
});

afterEach(() => {
    if (component) unmount(component);
    component = null;
    target.remove();
    activeTab.set('files');
    selectionBarState.set({ count: 0 });
    fileListView.set({ kind: 'state', stateKind: 'loading', title: 'Loading files' });
});

describe('scroll divider', () => {
    it('appears once the file list has moved under the bar', () => {
        expect(shell().classList.contains('is-scrolled')).toBe(false);

        scrollTo('#file-list', 40);

        expect(shell().classList.contains('is-scrolled')).toBe(true);
    });

    it('does not follow the file list onto another tab', () => {
        // The rule used to stay lit under Account because the list it was
        // watching had been left part-scrolled behind it.
        scrollTo('#file-list', 40);

        activeTab.set('account');
        flushSync();

        expect(shell().classList.contains('is-scrolled')).toBe(false);
    });

    it('comes back on returning to a list that is still scrolled', () => {
        scrollTo('#file-list', 40);
        activeTab.set('account');
        flushSync();

        activeTab.set('files');
        flushSync();

        expect(shell().classList.contains('is-scrolled')).toBe(true);
    });

    it('answers the gallery as well as the list', () => {
        // Photos scrolls its own surface inside the same region, and the bar
        // never drew a divider for it at all.
        sidebarState.update((current) => ({ ...current, virtualView: 'photos' }));
        flushSync();

        scrollTo('#gallery-view', 60);

        expect(shell().classList.contains('is-scrolled')).toBe(true);
    });
});

describe('mobile hierarchy', () => {
    it('keeps Upload/Create contextual to drive content instead of a fifth destination', () => {
        const action = target.querySelector('.mobile-context-action') as HTMLElement;
        expect(action.hidden).toBe(false);
        expect(shell().classList.contains('has-context-action')).toBe(true);
        expect(target.querySelectorAll('.tab-item')).toHaveLength(4);

        activeTab.set('account');
        flushSync();
        expect(action.hidden).toBe(true);
        expect(shell().classList.contains('has-context-action')).toBe(false);
    });

    it('marks the shell while selection actions reserve the last content row', () => {
        selectionBarState.set({ count: 1 });
        flushSync();
        expect(shell().classList.contains('is-selecting')).toBe(true);
        expect(shell().classList.contains('has-context-action')).toBe(false);
    });

    it('lets an empty folder own creation instead of duplicating its calls to action', () => {
        fileListView.set({
            kind: 'state',
            stateKind: 'empty',
            title: 'This folder is empty',
            actionLabel: 'Upload files',
            onAction: () => {},
            secondaryActionLabel: 'Create folder',
            onSecondaryAction: () => {},
        });
        flushSync();

        expect((target.querySelector('.mobile-context-action') as HTMLElement).hidden).toBe(true);
        expect(shell().classList.contains('has-context-action')).toBe(false);
    });
});
