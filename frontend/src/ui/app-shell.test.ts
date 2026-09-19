import { describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import AppShell from './AppShell.svelte';

describe('AppShell', () => {
    it('renders the semantic dashboard and its stable controller targets', () => {
        const { body } = render(AppShell, { props: { dashboardVisible: true } });

        for (const id of [
            'success-screen',
            'drives-nav',
            'notif-bell',
            'upload-btn',
            'profile-trigger',
            'breadcrumb-path',
            'selection-bar',
            'file-list',
            'gallery-view',
        ]) {
            expect(body).toContain(`id="${id}"`);
        }

        // Both drive lists render in the sidebar itself rather than waiting for
        // something to fill an empty host, so with no drives loaded yet the
        // shell already says what each section is doing.
        expect(body).toContain('Loading...');
        expect(body).toContain('No shared drives yet');
    });
});
