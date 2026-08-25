import { afterEach, describe, expect, it } from 'vitest';
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
            'mount-selection-modal',
        ]) {
            expect(host?.querySelector(`#${id}`)).not.toBeNull();
        }
    });

    it('keeps manual refresh and folder creation out of the header', () => {
        setup();

        expect(host?.querySelector('.header-actions .icon-btn')).toBeNull();
        expect(host?.querySelector('#new-folder-btn')).toBeNull();
    });

    it('places the mount action alongside shared-drive actions, not in the header', () => {
        setup();

        expect(host?.querySelector('.header-actions #mount-drive-button')).toBeNull();
        const actions = host?.querySelector('.drives-actions');
        const join = host?.querySelector('#open-join-drive');
        const mount = actions?.querySelector<HTMLButtonElement>('#mount-drive-button');

        expect(mount).not.toBeNull();
        expect(mount?.classList.contains('drive-action-btn')).toBe(true);
        expect(mount?.classList.contains('mount-sidebar-action')).toBe(true);
        expect(mount?.getAttribute('role')).toBeNull();
        if (!join || !mount) throw new Error('missing sidebar drive action');
        expect(join.compareDocumentPosition(mount) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
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
        expect(name?.querySelector('.sort-direction-up')).not.toBeNull();

        click('.file-sort-button.col-name');
        name = host?.querySelector<HTMLButtonElement>('.file-sort-button.col-name');
        expect(name?.getAttribute('aria-label')).toContain('ascending');
        expect(name?.querySelector('.sort-direction-down')).not.toBeNull();
    });
});
