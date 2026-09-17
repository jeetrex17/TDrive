import { afterEach, describe, expect, it } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import AppShell from './AppShell.svelte';
import { resetFileSortState } from './file-list/file-sort-store';
import { fileListColumnMode, resetFileListColumnMode } from './file-list/column-mode-store';

let app: Record<string, unknown> | null = null;
let host: HTMLElement | null = null;

function setup(dashboardVisible = true): void {
    host = document.createElement('div');
    document.body.appendChild(host);
    app = mount(AppShell, { target: host, props: { dashboardVisible } });
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
    resetFileListColumnMode();
    if (app) await unmount(app);
    host?.remove();
    app = null;
    host = null;
});

describe('AppShell behavior', () => {
    it('keeps semantic shell hosts stable for controller modules', () => {
        setup();

        for (const id of [
            'success-screen',
            'file-list',
            'gallery-view',
        ]) {
            expect(host?.querySelector(`#${id}`)).not.toBeNull();
        }
    });

    it('keeps the mounted dashboard hidden until the session is ready', () => {
        setup(false);
        const dashboard = host?.querySelector<HTMLElement>('#success-screen');
        expect(dashboard?.hidden).toBe(true);
        expect(dashboard?.getAttribute('aria-hidden')).toBe('true');
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

    it('leaves the mount control out of the sidebar on mobile', () => {
        // Go injects window._wails.environment before the bundle loads and the
        // platform helpers read its OS; keep the runtime's own hooks intact.
        const previous = window._wails;
        window._wails = { ...previous, environment: { OS: 'ios', Arch: 'arm64', Debug: false } };
        try {
            setup();

            expect(host?.querySelector('#open-join-drive')).not.toBeNull();
            expect(host?.querySelector('#mount-drive-button')).toBeNull();
        } finally {
            window._wails = previous;
        }
    });

    it('renders the selection bar island inside the actions column, hidden until rows are picked', () => {
        setup();

        const selectionBar = host?.querySelector<HTMLElement>('#selection-bar');
        expect(selectionBar).not.toBeNull();
        // The controller is what reveals the bar, so it must still start hidden
        // even though its contents are now rendered up front.
        expect(selectionBar?.style.display).toBe('none');
        expect(selectionBar?.querySelector('#selection-count')).not.toBeNull();
        expect(selectionBar?.querySelector('#selection-move')).not.toBeNull();
        expect(selectionBar?.querySelector('#selection-delete')).not.toBeNull();
    });

    it('leaves the selection bar empty until the dashboard is up', () => {
        setup(false);

        expect(host?.querySelector('#selection-bar')?.children).toHaveLength(0);
    });

    it('exposes sortable data-grid headers', () => {
        setup();

        const grid = host?.querySelector('#file-list');
        expect(grid?.getAttribute('role')).toBe('grid');
        expect(grid?.getAttribute('aria-colcount')).toBe('4');

        click('.col-name .file-sort-button');
        let name = host?.querySelector<HTMLButtonElement>('.col-name .file-sort-button');
        const nameHeader = host?.querySelector('.col-name');
        expect(name?.classList.contains('active')).toBe(true);
        expect(nameHeader?.getAttribute('role')).toBe('columnheader');
        expect(nameHeader?.getAttribute('aria-sort')).toBe('ascending');
        expect(name?.getAttribute('aria-label')).toContain('descending');
        expect(name?.querySelector('.sort-direction-up')).not.toBeNull();

        click('.col-name .file-sort-button');
        name = host?.querySelector<HTMLButtonElement>('.col-name .file-sort-button');
        expect(nameHeader?.getAttribute('aria-sort')).toBe('descending');
        expect(name?.getAttribute('aria-label')).toContain('ascending');
        expect(name?.querySelector('.sort-direction-down')).not.toBeNull();
    });
});

describe('the file list header during a search', () => {
    it('relabels the second column without destroying its sort control', () => {
        setup();

        const dateColumn = () => host?.querySelector('.file-table-header .col-date');
        const sortButton = () => dateColumn()?.querySelector('button.file-sort-button');

        expect(sortButton()).not.toBeNull();
        expect(dateColumn()?.textContent).toContain('Date');

        // A search spans every folder, so the column reports where each result
        // lives instead. Relabelling used to be a textContent write on this
        // column, which deleted the button inside it and never brought it back.
        fileListColumnMode.set('location');
        flushSync();
        expect(dateColumn()?.textContent).toContain('Location');
        expect(sortButton()).not.toBeNull();

        fileListColumnMode.set('date');
        flushSync();
        expect(dateColumn()?.textContent).toContain('Date');
        expect(sortButton()).not.toBeNull();
    });
});
