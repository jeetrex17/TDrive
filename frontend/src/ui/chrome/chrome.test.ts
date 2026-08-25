import { afterEach, describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import Avatar from './Avatar.svelte';
import Breadcrumb from './Breadcrumb.svelte';
import ProfileMenu from './ProfileMenu.svelte';
import UploadMenu from './UploadMenu.svelte';
import { breadcrumbPath, type BreadcrumbDrag } from './breadcrumb-store';
import { encryptionEntryVisible, profileLoaded, profileUser } from './profile-store';

const noop = () => {};

const drag: BreadcrumbDrag = {
    isActive: () => false,
    canDrop: () => false,
    highlight: noop,
    leave: noop,
    dropOn: noop,
    registerRoot: noop,
};

const breadcrumbProps = { onNavigate: noop, onBack: noop, drag };
const profileProps = { onOpen: noop, onEncryptionSettings: noop, onLogout: noop };
const uploadProps = { onFiles: noop, onFolder: noop };

afterEach(() => {
    breadcrumbPath.set([]);
    profileUser.set(null);
    profileLoaded.set(false);
    encryptionEntryVisible.set(false);
});

describe('Breadcrumb', () => {
    it('renders only a disabled root at the drive root', () => {
        const { body } = render(Breadcrumb, { props: breadcrumbProps });

        expect(body).toContain('My Drive');
        expect(body).not.toContain('breadcrumb-sep');
        expect(body).toContain('opacity: 0.35'); // back button dimmed
    });

    it('renders crumbs with separators and an enabled back button', () => {
        breadcrumbPath.set([
            { id: 'd:a', name: 'Docs' },
            { id: 'd:b', name: 'Taxes & <stuff>' },
        ]);

        const { body } = render(Breadcrumb, { props: breadcrumbProps });

        expect(body).toContain('Docs');
        expect(body).toContain('Taxes &amp; &lt;stuff>');
        expect((body.match(/breadcrumb-sep/g) || [])).toHaveLength(2);
        expect(body).toContain('opacity: 1');
        expect(body).toContain('data-index="1"');
    });
});

describe('Avatar', () => {
    it('renders a photo background when available', () => {
        const { body } = render(Avatar, { props: { user: { photo_base64: 'Zm9v' } } });
        expect(body).toContain('data:image/jpeg;base64,Zm9v');
    });

    it('falls back to initials on the deterministic palette', () => {
        const { body } = render(Avatar, { props: { user: { display_name: 'Ada Lovelace', user_id: 1 } } });
        expect(body).toContain('>AL</span>');
        expect(body).toContain('background: #bb9af7'); // 1 % 8 -> palette[1]
    });
});

describe('ProfileMenu', () => {
    it('shows the loading header until the profile hydrates', () => {
        const { body } = render(ProfileMenu, { props: profileProps });

        expect(body).toContain('Loading account…');
        expect(body).not.toContain('profile-menu-encryption-settings');
        expect(body).toContain('Mount Tdrive personal');
        expect(body).not.toContain('id="mount-selection-modal"');
        expect(body).toContain('Log out');
    });

    it('renders name, handle, and the encryption entry when enabled', () => {
        profileUser.set({ display_name: 'Ada L.', username: 'ada', user_id: 7 });
        profileLoaded.set(true);
        encryptionEntryVisible.set(true);

        const { body } = render(ProfileMenu, { props: profileProps });

        expect(body).toContain('Ada L.');
        expect(body).toContain('@ada');
        expect(body).toContain('Encryption settings');
    });

    it('places the accessible mount action below encryption settings', () => {
        encryptionEntryVisible.set(true);

        const { body } = render(ProfileMenu, { props: profileProps });
        const encryptionIndex = body.indexOf('id="profile-menu-encryption-settings"');
        const mountIndex = body.indexOf('id="mount-drive-button"');
        const logoutIndex = body.indexOf('id="profile-menu-logout"');

        expect(encryptionIndex).toBeGreaterThanOrEqual(0);
        expect(mountIndex).toBeGreaterThan(encryptionIndex);
        expect(mountIndex).toBeLessThan(logoutIndex);
        expect(body.slice(mountIndex, logoutIndex)).toContain('role="menuitem"');
    });
});

describe('UploadMenu', () => {
    it('renders the trigger and a hidden menu with both items', () => {
        const { body } = render(UploadMenu, { props: uploadProps });

        expect(body).toContain('id="upload-btn"');
        expect(body).toContain('aria-expanded="false"');
        expect(body).toContain('display: none');
        expect(body).toContain('id="upload-menu-files"');
        expect(body).toContain('id="upload-menu-folder"');
    });
});
