import { describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import AppShell from './AppShell.svelte';

describe('AppShell', () => {
    it('renders the semantic dashboard and its stable controller targets', () => {
        const { body } = render(AppShell, { props: { dashboardVisible: true } });

        for (const id of [
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
        ]) {
            expect(body).toContain(`id="${id}"`);
        }
    });
});
