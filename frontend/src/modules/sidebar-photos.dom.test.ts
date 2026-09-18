import { afterEach, describe, expect, it } from 'vitest';
import { get } from 'svelte/store';
import { state } from '../state';
import { sidebarState, setSidebarState } from '../ui/sidebar/sidebar-store';
import { setPhotosMode } from './gallery';

afterEach(() => {
    document.body.replaceChildren();
    state.activeChannel = null;
    setSidebarState({
        personal: [],
        shared: [],
        pending: [],
        activeChannelId: null,
        virtualView: null,
    });
});

describe('Photos sidebar route semantics', () => {
    it('moves aria-current between Photos and the active drive as the view changes', () => {
        document.body.innerHTML = `
            <main class="main-content"></main>
            <button id="nav-photos" type="button">Photos</button>
            <button class="drive-item active" data-channel-id="42" aria-current="page">Family archive</button>
        `;
        state.activeChannel = { id: 42, title: 'Family archive', kind: 'shared' };
        setSidebarState({
            personal: [],
            shared: [{
                id: 42,
                title: 'Family archive',
                kind: 'shared',
                isActive: true,
                inviteLink: '',
            }],
            pending: [],
            activeChannelId: 42,
            virtualView: null,
        });

        setPhotosMode(true);

        const photos = document.getElementById('nav-photos');
        const drive = document.querySelector<HTMLElement>('[data-channel-id="42"]');
        expect(photos?.getAttribute('aria-current')).toBe('page');
        expect(drive?.hasAttribute('aria-current')).toBe(false);
        expect(get(sidebarState).virtualView).toBe('photos');

        setPhotosMode(false);

        expect(photos?.hasAttribute('aria-current')).toBe(false);
        expect(drive?.getAttribute('aria-current')).toBe('page');
        expect(get(sidebarState).virtualView).toBe(null);
    });
});
