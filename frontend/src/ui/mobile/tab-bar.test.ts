import { afterEach, describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import TabBar from './TabBar.svelte';
import { profileLoaded, profileUser } from '../chrome/profile-store';

const noop = () => {};
const props = { active: 'files' as const, transferBadge: 0, onSelect: noop };

afterEach(() => {
    profileUser.set(null);
    profileLoaded.set(false);
});

describe('TabBar account cell', () => {
    it('draws an icon until the profile arrives', () => {
        // An empty disc while the fetch is in flight reads as a fault rather
        // than as loading, so the icon holds the place.
        const { body } = render(TabBar, { props });

        expect(body).not.toContain('profile-avatar');
        expect(body).toContain('Account');
    });

    it('shows the person once their profile is known', () => {
        profileUser.set({ displayName: 'Ada Lovelace', username: 'ada', userId: 7 });
        profileLoaded.set(true);

        const { body } = render(TabBar, { props });

        expect(body).toContain('profile-avatar');
        expect(body).toContain('AL');
    });

    it('carries the photo when the account has one', () => {
        profileUser.set({ displayName: 'Ada Lovelace', photoBase64: 'QUJD', userId: 7 });
        profileLoaded.set(true);

        const { body } = render(TabBar, { props });

        expect(body).toContain('data:image/jpeg;base64,QUJD');
    });

    it('keeps four destinations around the upload cell', () => {
        // The avatar replaces a glyph, not a cell: five cells is what keeps the
        // row's spacing even. Only the upload host renders here, because the
        // trigger itself is portalled into it at runtime.
        const { body } = render(TabBar, { props });

        for (const label of ['Files', 'Photos', 'Transfers', 'Account']) {
            expect(body).toContain(label);
        }
        expect(body).toContain('upload-menu-root');
        // The host sits between the second and third destination, which is what
        // puts it in the middle of the row.
        expect(body.indexOf('Photos')).toBeLessThan(body.indexOf('upload-menu-root'));
        expect(body.indexOf('upload-menu-root')).toBeLessThan(body.indexOf('Transfers'));
    });
});
