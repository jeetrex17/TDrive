import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';

const mocks = vi.hoisted(() => ({
    mobile: true,
    index: vi.fn(),
}));
vi.mock('../api', () => ({ isMobilePlatform: () => mocks.mobile }));
vi.mock('./folder-index', () => ({ refreshFolderIndex: mocks.index }));

import { state } from '../state';
import { activeTab } from '../ui/mobile/mobile-shell-store';
import { breadcrumbPath } from '../ui/chrome/breadcrumb-store';
import { PLACE_STORAGE_KEY, rememberPlace, reloadLaunchPlace, restorePlace } from './place-memory';

const folders = new Map([
    ['d:work', { id: 'd:work', name: 'Work', parentId: '' }],
    ['d:taxes', { id: 'd:taxes', name: 'Taxes', parentId: 'd:work' }],
]);

function store(place: unknown): void {
    window.localStorage.setItem(PLACE_STORAGE_KEY, JSON.stringify(place));
    reloadLaunchPlace();
}

beforeEach(() => {
    const storage = new Map<string, string>();
    vi.stubGlobal('localStorage', {
        getItem: (key: string) => storage.get(key) ?? null,
        setItem: (key: string, value: string) => storage.set(key, value),
        removeItem: (key: string) => storage.delete(key),
    });
    mocks.mobile = true;
    mocks.index.mockResolvedValue({ folders: [...folders.values()], byId: folders, children: new Map() });
    state.activeChannel = { id: 7, title: 'My Drive', kind: 'personal' };
    state.folderPath = [];
    state.currentFolderId = '';
    activeTab.set('files');
    breadcrumbPath.set([]);
});

afterEach(() => {
    vi.unstubAllGlobals();
    state.activeChannel = null;
});

describe('place memory', () => {
    it('reopens the tab and folder the last launch left', async () => {
        store({ tab: 'transfers', driveId: 7, path: [{ id: 'd:work', name: 'Work' }, { id: 'd:taxes', name: 'Taxes' }] });

        await restorePlace();

        expect(state.currentFolderId).toBe('d:taxes');
        expect(get(breadcrumbPath).map((entry) => entry.name)).toEqual(['Work', 'Taxes']);
        expect(get(activeTab)).toBe('transfers');
    });

    it('stays at the root when a folder on the way is gone', async () => {
        store({ tab: 'files', driveId: 7, path: [{ id: 'd:work', name: 'Work' }, { id: 'd:gone', name: 'Gone' }] });

        await restorePlace();

        expect(state.folderPath).toEqual([]);
        expect(state.currentFolderId).toBe('');
    });

    it('ignores a place recorded in another drive, and restores only once', async () => {
        store({ tab: 'account', driveId: 8, path: [] });
        await restorePlace();
        expect(get(activeTab)).toBe('files');

        store({ tab: 'account', driveId: 7, path: [] });
        await restorePlace();
        activeTab.set('files');
        await restorePlace();
        expect(get(activeTab)).toBe('files');
    });

    it('records the tab and path as they change', () => {
        const stop = rememberPlace();
        activeTab.set('photos');
        breadcrumbPath.set([{ id: 'd:work', name: 'Work' }]);
        stop();

        expect(JSON.parse(window.localStorage.getItem(PLACE_STORAGE_KEY) ?? 'null')).toEqual({
            tab: 'photos', driveId: 7, path: [{ id: 'd:work', name: 'Work' }],
        });
    });
});
