import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import AppShell from './AppShell.svelte';
import { resetFileSortState } from './file-list/file-sort-store';

let app: Record<string, unknown> | null = null;
let host: HTMLElement | null = null;

function setup(): void {
    host = document.createElement('div');
    document.body.appendChild(host);
    app = mount(AppShell, { target: host, props: {} });
    flushSync();
}

function click(selector: string): void {
    const button = host?.querySelector<HTMLButtonElement>(selector);
    if (!button) throw new Error(`missing ${selector}`);
    button.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    flushSync();
}

afterEach(async () => {
    resetFileSortState();
    delete (window as unknown as { triggerRefresh?: unknown }).triggerRefresh;
    delete (window as unknown as { openNewFolderModal?: unknown }).openNewFolderModal;
    if (app) await unmount(app);
    host?.remove();
    app = null;
    host = null;
});

describe('AppShell behavior', () => {
    it('keeps the shell hosts stable for the controller modules', () => {
        setup();

        for (const id of [
            'auth-wrapper',
            'success-screen',
            'file-list',
            'gallery-view',
            'context-menu',
            'preview-modal',
            'video-modal',
        ]) {
            expect(host?.querySelector(`#${id}`)).not.toBeNull();
        }
    });

    it('routes header actions through the existing app controller hooks', () => {
        const triggerRefresh = vi.fn();
        const openNewFolderModal = vi.fn();
        (window as unknown as { triggerRefresh: () => void }).triggerRefresh = triggerRefresh;
        (window as unknown as { openNewFolderModal: () => void }).openNewFolderModal = openNewFolderModal;
        setup();

        click('.header-actions .icon-btn');
        click('#new-folder-btn');

        expect(triggerRefresh).toHaveBeenCalledTimes(1);
        expect(openNewFolderModal).toHaveBeenCalledTimes(1);
    });

    it('keeps the mount action out of the header', () => {
        setup();

        expect(host?.querySelector('.header-actions #mount-drive-button')).toBeNull();
        expect(host?.querySelector('#mount-control-root')).toBeNull();
    });

    it('leaves selection bar contents owned by the Svelte SelectionBar island', () => {
        setup();

        const selectionBar = host?.querySelector('#selection-bar');
        expect(selectionBar).not.toBeNull();
        expect(selectionBar?.children).toHaveLength(0);
    });

    it('toggles file-list sort headers accessibly', () => {
        setup();

        click('.file-sort-button.col-name');
        let name = host?.querySelector<HTMLButtonElement>('.file-sort-button.col-name');
        expect(name?.classList.contains('active')).toBe(true);
        expect(name?.getAttribute('aria-pressed')).toBe('true');
        expect(name?.getAttribute('aria-label')).toContain('descending');
        expect(name?.textContent).toContain('↑');

        click('.file-sort-button.col-name');
        name = host?.querySelector<HTMLButtonElement>('.file-sort-button.col-name');
        expect(name?.getAttribute('aria-label')).toContain('ascending');
        expect(name?.textContent).toContain('↓');
    });
});
