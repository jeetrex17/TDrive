import { describe, it, expect, beforeEach } from 'vitest';
import { get } from 'svelte/store';
import { sidebarState } from '../sidebar/sidebar-store';
import { fileListView } from '../file-list/file-list-store';
import { historyEvents } from '../notifications/notif-store';
import type { DriveChannel } from '../../types';
import {
    activeDrive,
    activeTransferCount,
    downloadSharePaths,
    driveSwitcherOpen,
    fileListCount,
    rememberDownloadSharePath,
} from './mobile-shell-store';

function drive(id: number, title: string, kind: 'personal' | 'shared'): DriveChannel {
    return { id, title, kind, isActive: kind === 'personal', inviteLink: '' } as DriveChannel;
}

describe('mobile-shell-store', () => {
    beforeEach(() => {
        sidebarState.set({ personal: [], shared: [], pending: [], activeChannelId: null, photosActive: false });
        fileListView.set({ kind: 'state', stateKind: 'loading', title: 'Loading' });
        historyEvents.set([]);
        downloadSharePaths.set(new Map());
        driveSwitcherOpen.set(false);
    });

    it('resolves the active drive from the sidebar snapshot', () => {
        expect(get(activeDrive)).toBeNull();
        sidebarState.set({
            personal: [drive(1, 'My Drive', 'personal')],
            shared: [drive(2, 'Team assets', 'shared')],
            pending: [],
            activeChannelId: 2,
            photosActive: false,
        });
        expect(get(activeDrive)?.title).toBe('Team assets');
    });

    it('counts only loaded file rows for the header', () => {
        expect(get(fileListCount)).toBe(0);
        fileListView.set({ kind: 'rows', rows: [
            { kind: 'file', selectionKey: 'a' },
            { kind: 'folder', selectionKey: 'b' },
        ] as never });
        expect(get(fileListCount)).toBe(2);
    });

    it('counts active transfers for the tab badge', () => {
        expect(get(activeTransferCount)).toBe(0);
        historyEvents.set([
            { kind: 'transfer', id: 'xfer:up:1', direction: 'up', name: 'a', progress: 10, total: 0, bytes: 0, speed: 0, status: 'active', startedAt: 0, finishedAt: 0 },
            { kind: 'transfer', id: 'xfer:up:2', direction: 'up', name: 'b', progress: 100, total: 0, bytes: 0, speed: 0, status: 'done', startedAt: 0, finishedAt: 1 },
        ]);
        expect(get(activeTransferCount)).toBe(1);
    });

    it('remembers a download share path and ignores empty paths', () => {
        rememberDownloadSharePath('xfer:down:file:9', '/sandbox/Downloads/a.pdf');
        rememberDownloadSharePath('xfer:down:file:10', '');
        const paths = get(downloadSharePaths);
        expect(paths.get('xfer:down:file:9')).toBe('/sandbox/Downloads/a.pdf');
        expect(paths.has('xfer:down:file:10')).toBe(false);
    });
});
