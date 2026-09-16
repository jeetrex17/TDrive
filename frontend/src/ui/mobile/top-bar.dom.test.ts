// The phone top bar's two modes: the drive header, and the selection header
// that replaces it. Both are about what one tap does.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import { get } from 'svelte/store';

const clearSelection = vi.hoisted(() => vi.fn());
vi.mock('../../modules/selection', () => ({ clearSelection }));
vi.mock('../../modules/navigation', () => ({ navigateBack: vi.fn(), navigateToIndex: vi.fn() }));
vi.mock('../../modules/search', () => ({ clearSearch: vi.fn() }));

import TopBar from './TopBar.svelte';
import { selectionBarState } from '../selection/selection-bar-store';
import { sidebarState } from '../sidebar/sidebar-store';
import { activeTab, driveSwitcherOpen, driveSyncStatus } from './mobile-shell-store';

let target: HTMLElement;
let component: Record<string, unknown> | null = null;

function render(): void {
    target = document.createElement('div');
    document.body.append(target);
    component = mount(TopBar, { target, props: { active: 'files' as const } });
    flushSync();
}

beforeEach(() => {
    sidebarState.update((current) => ({
        ...current,
        activeChannelId: 1,
        personal: [{ id: 1, title: 'Camera roll', kind: 'personal' } as never],
    }));
    driveSyncStatus.set('syncing');
    selectionBarState.set({ count: 0 });
    activeTab.set('files');
    driveSwitcherOpen.set(false);
    clearSelection.mockClear();
});

afterEach(() => {
    if (component) unmount(component);
    component = null;
    target?.remove();
});

describe('drive header', () => {
    it('keeps the sync ring out of the switcher button', () => {
        // Nested, the ring's click bubbled into the header's: one tap jumped to
        // Transfers and opened the drive switcher on top of it.
        render();

        const header = target.querySelector('.drive-header-btn') as HTMLElement;
        const ring = target.querySelector('.sync-ring') as HTMLElement;

        expect(ring.tagName).toBe('BUTTON');
        expect(header.contains(ring)).toBe(false);
    });

    it('opens the queue alone when the ring is tapped', () => {
        render();

        const ring = target.querySelector('.sync-ring') as HTMLElement;
        ring.click();
        flushSync();

        expect(get(activeTab)).toBe('transfers');
        expect(get(driveSwitcherOpen)).toBe(false);
    });

    it('opens the switcher alone when the name is tapped', () => {
        render();

        (target.querySelector('.drive-header-btn') as HTMLElement).click();
        flushSync();

        expect(get(driveSwitcherOpen)).toBe(true);
        expect(get(activeTab)).toBe('files');
    });
});

describe('selection header', () => {
    it('stays out of the way until something is selected', () => {
        render();

        const selection = target.querySelector('.topbar-selection') as HTMLElement;
        expect(selection.hidden).toBe(true);
        expect((target.querySelector('.topbar-files') as HTMLElement).hidden).toBe(false);
    });

    it('replaces the bar with the count and a way out', () => {
        render();
        selectionBarState.set({ count: 3 });
        flushSync();

        const selection = target.querySelector('.topbar-selection') as HTMLElement;
        expect(selection.hidden).toBe(false);
        expect(selection.textContent).toContain('3 selected');
        expect((target.querySelector('.topbar-files') as HTMLElement).hidden).toBe(true);
    });

    it('counts a single row in the singular', () => {
        render();
        selectionBarState.set({ count: 1 });
        flushSync();

        expect((target.querySelector('.topbar-selection') as HTMLElement).textContent)
            .toContain('1 selected');
    });

    it('leaves the mode when Done is tapped', () => {
        // The tab bar is hidden while selecting and an iPhone has no hardware
        // BACK, so this button is the only exit.
        render();
        selectionBarState.set({ count: 2 });
        flushSync();

        (target.querySelector('.topbar-done') as HTMLElement).click();

        expect(clearSelection).toHaveBeenCalledTimes(1);
    });

    it('keeps the search field mounted while selecting', () => {
        // The search controller binds to #search-input once at startup; losing
        // the node loses the binding.
        render();
        selectionBarState.set({ count: 2 });
        flushSync();

        expect(target.querySelector('#search-input')).not.toBeNull();
    });
});
