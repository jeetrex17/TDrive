import { afterEach, describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import Avatar from './Avatar.svelte';
import Breadcrumb from './Breadcrumb.svelte';
import ProfileMenu from './ProfileMenu.svelte';
import UploadMenu from './UploadMenu.svelte';
import { breadcrumbPath, type BreadcrumbDrag } from './breadcrumb-store';
import { sidebarState } from '../sidebar/sidebar-store';
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
const profileProps = { onOpen: noop, onLogout: noop };
const uploadProps = { onFiles: noop, onFolder: noop };

afterEach(() => {
    breadcrumbPath.set([]);
    sidebarState.set({ personal: [], shared: [], pending: [], activeChannelId: null, virtualView: null });
    profileUser.set(null);
    profileLoaded.set(false);
    encryptionEntryVisible.set(false);
});

describe('Breadcrumb', () => {
    it('renders only a disabled root at the drive root', () => {
        const { body } = render(Breadcrumb, { props: breadcrumbProps });

        expect(body).toContain('My Drive');
        expect(body).not.toContain('breadcrumb-sep');
        expect(body).toContain('is-hidden'); // back button held in place, invisible
        expect(body).toContain('aria-current="location"');
    });

    it('names the root after the active shared drive', () => {
        sidebarState.set({
            personal: [{ id: 1, title: 'My Drive', kind: 'personal' }],
            shared: [{ id: 2, title: 'Team assets', kind: 'shared' }],
            pending: [],
            activeChannelId: 2,
            virtualView: null,
        } as never);

        const { body } = render(Breadcrumb, { props: breadcrumbProps });

        expect(body).toContain('Team assets');
        expect(body).not.toContain('My Drive');
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
        expect(body).not.toContain('is-hidden');
        expect(body).toContain('aria-label="Folder path"');
        expect((body.match(/aria-current="location"/g) || [])).toHaveLength(1);
        expect(body).toContain('data-index="1"');
    });

    it('offers the path field only when it can open a path', () => {
        const plain = render(Breadcrumb, { props: breadcrumbProps }).body;
        expect(plain).not.toContain('breadcrumb-edit');
        expect(plain).not.toContain('is-editable');

        const { body } = render(Breadcrumb, { props: { ...breadcrumbProps, onPathSubmit: async () => null } });
        expect(body).toMatch(/id="breadcrumb-edit"[^>]*aria-label="Edit path"/);
        expect(body).toMatch(/aria-keyshortcuts="(Control\+L|Meta\+L)"/);
        expect(body).toContain('is-editable');
        // The trail renders until the field is opened.
        expect(body).not.toContain('breadcrumb-path-input');
    });
});

describe('Breadcrumb history buttons', () => {
    it('are absent until there is a visited folder to return to', () => {
        const { body } = render(Breadcrumb, { props: breadcrumbProps });
        expect(body).not.toContain('breadcrumb-history-back');
        expect(body).not.toContain('breadcrumb-history-forward');
    });

    it('render as a pair with the unavailable direction disabled', () => {
        const { body } = render(Breadcrumb, { props: { ...breadcrumbProps, canGoBack: true } });
        expect(body).toMatch(/id="breadcrumb-history-back"[^>]*aria-label="Back"/);
        expect(body).not.toMatch(/id="breadcrumb-history-back"[^>]*disabled/);
        expect(body).toMatch(/id="breadcrumb-history-forward"[^>]*disabled/);
        expect(body).toMatch(/aria-keyshortcuts="(Alt\+ArrowLeft|Meta\+BracketLeft)"/);
    });
});

describe('Avatar', () => {
    it('renders a photo background when available', () => {
        const { body } = render(Avatar, { props: { user: { photoBase64: 'Zm9v' } } });
        expect(body).toContain('data:image/jpeg;base64,Zm9v');
    });

    it('falls back to initials on the deterministic palette', () => {
        const { body } = render(Avatar, { props: { user: { displayName: 'Ada Lovelace', userId: 1 } } });
        expect(body).toContain('>AL</span>');
        expect(body).toContain('background: #bb9af7'); // 1 % 8 -> palette[1]
    });
});

describe('ProfileMenu', () => {
    it('shows the loading header until the profile hydrates', () => {
        const { body } = render(ProfileMenu, { props: profileProps });

        expect(body).toContain('Loading account…');
        expect(body).not.toContain('profile-menu-encryption-settings');
        expect(body).not.toContain('id="mount-drive-button"');
        expect(body).not.toContain('id="mount-selection-modal"');
        expect(body).toContain('Log out');
    });

    it('renders the name and handle above the Settings entry', () => {
        profileUser.set({ displayName: 'Ada L.', username: 'ada', userId: 7 });
        profileLoaded.set(true);

        const { body } = render(ProfileMenu, { props: profileProps });

        expect(body).toContain('Ada L.');
        expect(body).toContain('@ada');
        expect(body).toContain('id="profile-menu-settings"');
    });

    it('keeps only Settings, updates and log out, in that order', () => {
        encryptionEntryVisible.set(true);

        const { body } = render(ProfileMenu, { props: profileProps });
        const order = ['profile-menu-settings', 'profile-menu-updates', 'profile-menu-logout']
            .map((id) => body.indexOf(`id="${id}"`));

        expect(order.every((index) => index >= 0)).toBe(true);
        expect([...order].sort((a, b) => a - b)).toEqual(order);
        for (const moved of ['appearance', 'photo-backup', 'encrypt-uploads', 'encryption-settings']) {
            expect(body).not.toContain(`id="profile-menu-${moved}"`);
        }
        expect(body).not.toContain('id="mount-drive-button"');
    });

    it('leaves out the updates entry where the updater is unavailable', () => {
        const { body } = render(ProfileMenu, { props: { ...profileProps, updaterAvailable: false } });

        expect(body).not.toContain('id="profile-menu-updates"');
        expect(body).toContain('id="profile-menu-settings"');
        expect(body).toContain('id="profile-menu-logout"');
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

    it('offers only file uploads without a folder handler', () => {
        const { body } = render(UploadMenu, { props: { onFiles: noop } });

        expect(body).toContain('id="upload-menu-files"');
        expect(body).not.toContain('id="upload-menu-folder"');
    });

    it('adds New folder below a separator and labels the trigger "New"', () => {
        const { body } = render(UploadMenu, { props: { onFiles: noop, onFolder: noop, onNewFolder: noop } });

        expect(body).toContain('id="upload-menu-new-folder"');
        // Order: upload files, upload folder, separator, then New folder.
        const files = body.indexOf('id="upload-menu-files"');
        const folder = body.indexOf('id="upload-menu-folder"');
        const sep = body.indexOf('upload-menu-sep');
        const newFolder = body.indexOf('id="upload-menu-new-folder"');
        expect(files).toBeLessThan(folder);
        expect(folder).toBeLessThan(sep);
        expect(sep).toBeLessThan(newFolder);

        // The trigger now covers both upload and create, so it reads "New".
        const trigger = body.match(/id="upload-btn"[\s\S]*?<\/button>/)?.[0] ?? '';
        expect(trigger).toContain('New');
        expect(trigger).not.toContain('Upload');
    });
});
