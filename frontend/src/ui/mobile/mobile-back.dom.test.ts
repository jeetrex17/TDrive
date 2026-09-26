// The Android BACK contract: the host asks the page what to dismiss and only
// leaves the app when the page reports the press unhandled.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';

const exitPhotos = vi.hoisted(() => vi.fn());
const navigateBack = vi.hoisted(() => vi.fn());
const clearSelection = vi.hoisted(() => vi.fn());
vi.mock('../../modules/gallery', () => ({ exitPhotos }));
vi.mock('../../modules/navigation', () => ({ navigateBack }));
vi.mock('../../modules/selection', () => ({ clearSelection }));
const closeTrash = vi.hoisted(() => vi.fn());
vi.mock('../../modules/trash/controller', () => ({ closeTrash }));

import { breadcrumbPath } from '../chrome/breadcrumb-store';
import { closeTopSheet, pushSheet } from '../modals/sheet-stack';
import { selectionBarState } from '../selection/selection-bar-store';
import { sidebarState } from '../sidebar/sidebar-store';
import { activateMobileBack, BACK_BRIDGE, handleBackPress } from './mobile-back';
import { activeTab, driveSwitcherOpen } from './mobile-shell-store';

beforeEach(() => {
    while (closeTopSheet());
    driveSwitcherOpen.set(false);
    sidebarState.update((current) => ({ ...current, virtualView: null }));
    selectionBarState.set({ count: 0 });
    activeTab.set('files');
    breadcrumbPath.set([]);
    exitPhotos.mockClear();
    navigateBack.mockClear();
    clearSelection.mockClear();
    closeTrash.mockClear();
});

afterEach(() => {
    delete window[BACK_BRIDGE];
});

describe('android back', () => {
    it('leaves the app at the drive root with nothing open', () => {
        expect(handleBackPress()).toBe(false);
        expect(exitPhotos).not.toHaveBeenCalled();
        expect(navigateBack).not.toHaveBeenCalled();
    });

    // The trash has no breadcrumb to unwind, so before it was named here BACK
    // fell all the way through and left the app from inside it.
    it('closes the trash instead of leaving the app', () => {
        sidebarState.update((current) => ({ ...current, virtualView: 'trash' }));
        expect(handleBackPress()).toBe(true);
        expect(closeTrash).toHaveBeenCalledTimes(1);
        expect(exitPhotos).not.toHaveBeenCalled();
    });

    it('returns to the files tab from another tab and closes the trash with it', () => {
        sidebarState.update((current) => ({ ...current, virtualView: 'trash' }));
        activeTab.set('account');
        expect(handleBackPress()).toBe(true);
        expect(get(activeTab)).toBe('files');
        expect(closeTrash).toHaveBeenCalledTimes(1);
    });

    it('closes an open sheet before anything underneath it', () => {
        const close = vi.fn();
        pushSheet(close);
        sidebarState.update((current) => ({ ...current, virtualView: 'photos' }));
        breadcrumbPath.set([{ id: 'd:1', name: 'Reports' }]);

        expect(handleBackPress()).toBe(true);
        expect(close).toHaveBeenCalledOnce();
        expect(exitPhotos).not.toHaveBeenCalled();
        expect(navigateBack).not.toHaveBeenCalled();
    });

    it('closes stacked sheets one press at a time, top first', () => {
        const order: string[] = [];
        pushSheet(() => order.push('player'));
        pushSheet(() => order.push('menu'));

        expect(handleBackPress()).toBe(true);
        expect(handleBackPress()).toBe(true);
        expect(order).toEqual(['menu', 'player']);
        expect(handleBackPress()).toBe(false);
    });

    it('closes the drive switcher before leaving the gallery', () => {
        driveSwitcherOpen.set(true);
        sidebarState.update((current) => ({ ...current, virtualView: 'photos' }));

        expect(handleBackPress()).toBe(true);
        expect(exitPhotos).not.toHaveBeenCalled();
    });

    it('cancels a selection before popping a folder', () => {
        selectionBarState.set({ count: 3 });
        breadcrumbPath.set([{ id: 'd:1', name: 'Reports' }]);

        expect(handleBackPress()).toBe(true);
        expect(clearSelection).toHaveBeenCalledOnce();
        expect(navigateBack).not.toHaveBeenCalled();
    });

    it('cancels empty selection mode before leaving the app', () => {
        selectionBarState.set({ count: 0, active: true });

        expect(handleBackPress()).toBe(true);
        expect(clearSelection).toHaveBeenCalledOnce();
    });

    it('returns to the files tab from transfers', () => {
        activeTab.set('transfers');

        expect(handleBackPress()).toBe(true);
        expect(get(activeTab)).toBe('files');
    });

    it('returns to the files tab from the account tab', () => {
        activeTab.set('account');
        breadcrumbPath.set([{ id: 'd:1', name: 'Reports' }]);

        expect(handleBackPress()).toBe(true);
        expect(get(activeTab)).toBe('files');
        // The folder the files tab was left in is still the folder it returns to.
        expect(navigateBack).not.toHaveBeenCalled();
    });

    it('lands on files, not the gallery, when back leaves a tab', () => {
        sidebarState.update((current) => ({ ...current, virtualView: 'photos' }));
        activeTab.set('transfers');

        expect(handleBackPress()).toBe(true);
        expect(exitPhotos).toHaveBeenCalledOnce();
        expect(get(activeTab)).toBe('files');
    });

    it('leaves the gallery before popping a folder', () => {
        sidebarState.update((current) => ({ ...current, virtualView: 'photos' }));
        breadcrumbPath.set([{ id: 'd:1', name: 'Reports' }]);

        expect(handleBackPress()).toBe(true);
        expect(exitPhotos).toHaveBeenCalledOnce();
        expect(navigateBack).not.toHaveBeenCalled();
    });

    it('pops one folder level inside a drive', () => {
        breadcrumbPath.set([{ id: 'd:1', name: 'Reports' }]);

        expect(handleBackPress()).toBe(true);
        expect(navigateBack).toHaveBeenCalledOnce();
    });

    it('publishes the bridge only while the shell is mounted', () => {
        const dispose = activateMobileBack();
        expect(typeof window[BACK_BRIDGE]).toBe('function');
        expect(window[BACK_BRIDGE]?.()).toBe(false);

        dispose();
        expect(window[BACK_BRIDGE]).toBeUndefined();
    });
});
