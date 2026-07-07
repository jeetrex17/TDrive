import { describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import AppShell from './AppShell.svelte';

describe('AppShell', () => {
    it('renders the stable hosts the TypeScript modules mount into', () => {
        const { body } = render(AppShell);

        for (const id of [
            'auth-wrapper',
            'success-screen',
            'drives-nav',
            'drives-personal',
            'drives-shared',
            'notif-bell-root',
            'upload-menu-root',
            'profile-root',
            'breadcrumb-root',
            'selection-bar',
            'file-list',
            'gallery-view',
            'context-menu',
            'preview-modal',
            'video-modal',
            'logout-modal',
        ]) {
            expect(body).toContain(`id="${id}"`);
        }
    });
});
