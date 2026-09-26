import { writable } from 'svelte/store';
import { describe, expect, it, vi } from 'vitest';
import type { AppView } from '../../ui/app/app-store';
import { activatePhotoBackupOnDashboard } from './activation';

describe('photo backup startup', () => {
    it('waits for the dashboard before starting Telegram-backed work', () => {
        const view = writable<AppView>({ kind: 'startup' });
        const stopBackup = vi.fn();
        const startBackup = vi.fn(() => stopBackup);
        const stopWatching = activatePhotoBackupOnDashboard(view, startBackup);

        view.set({ kind: 'auth', screen: 'phone' });
        view.set({ kind: 'auth', screen: 'drive' });
        expect(startBackup).not.toHaveBeenCalled();

        view.set({ kind: 'dashboard' });
        view.set({ kind: 'dashboard' });
        expect(startBackup).toHaveBeenCalledOnce();

        view.set({ kind: 'auth', screen: 'phone' });
        expect(stopBackup).toHaveBeenCalledOnce();

        view.set({ kind: 'dashboard' });
        expect(startBackup).toHaveBeenCalledTimes(2);

        stopWatching();
        expect(stopBackup).toHaveBeenCalledTimes(2);
        view.set({ kind: 'dashboard' });
        expect(startBackup).toHaveBeenCalledTimes(2);
    });
});
