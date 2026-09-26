<script lang="ts">
    import { tick } from 'svelte';
    import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
    import ChevronLeftIcon from '@lucide/svelte/icons/chevron-left';
    import FolderIcon from '@lucide/svelte/icons/folder';
    import FolderPlusIcon from '@lucide/svelte/icons/folder-plus';
    import HardDriveIcon from '@lucide/svelte/icons/hard-drive';
    import Link2Icon from '@lucide/svelte/icons/link-2';
    import LockKeyholeIcon from '@lucide/svelte/icons/lock-keyhole';
    import LogOutIcon from '@lucide/svelte/icons/log-out';
    import PaletteIcon from '@lucide/svelte/icons/palette';
    import CloudUploadIcon from '@lucide/svelte/icons/cloud-upload';
    import Trash2Icon from '@lucide/svelte/icons/trash-2';
    import Avatar from '../chrome/Avatar.svelte';
    import { getStorageUsed } from '../../api';
    import { formatBytes } from '../../utils';
    import { encryptionEntryVisible, profileLoaded, profileUser } from '../chrome/profile-store';
    import { sidebarState } from '../sidebar/sidebar-store';
    import AppearancePanel from '../theme/AppearancePanel.svelte';
    import PhotoCachePanel from '../gallery/PhotoCachePanel.svelte';
    import PhotoBackupPanel from '../gallery/PhotoBackupPanel.svelte';
    import { getThemeDefinition } from '../theme/theme-model';
    import { themeState } from '../theme/theme-controller';
    import { switchActiveChannel } from '../../modules/channels';
    import { openEncryptionSettingsModal } from '../../modules/modals/encryption-settings';
    import { openJoinDriveModal } from '../../modules/modals/join-drive';
    import { openNewDriveModal } from '../../modules/modals/new-drive';
    import { openLogoutModal } from '../../modules/modals/logout';
    import { openTrash } from '../../modules/trash/controller';
    import { activeTab as mobileActiveTab } from './mobile-shell-store';
    import type { DriveChannel } from '../../types';
    import { activeDrive, activeTab } from './mobile-shell-store';
    import { pushSheet } from '../modals/sheet-stack';
    import { photoBackupState } from '../../modules/photo-backup/controller';
    import { describeBackup } from '../gallery/photo-backup-view';

    const displayName = $derived(
        !$profileLoaded ? 'Loading account' : $profileUser?.displayName || 'Telegram account',
    );
    const handle = $derived(($profileUser?.username || '').trim());
    const drives = $derived([...$sidebarState.personal, ...$sidebarState.shared]);
    /**
     * The settings that are a screen rather than a row: each opens in place,
     * over the account list, with its own title and a way back. Backup earned
     * one the way Appearance did -- it is a switch with a page behind it, and
     * unfolding that page at the bottom of a list of unrelated rows left the
     * thing being configured off screen.
     */
    type AccountDetail = 'appearance' | 'backup';
    const DETAIL_TITLES: Record<AccountDetail, string> = {
        appearance: 'Appearance',
        backup: 'Photo & video backup',
    };

    let detail = $state<AccountDetail | null>(null);
    let appearanceOpener = $state<HTMLButtonElement | null>(null);
    let backupOpener = $state<HTMLButtonElement | null>(null);
    const currentThemeName = $derived(getThemeDefinition($themeState.resolvedThemeId).name);
    const appearanceSummary = $derived(
        $themeState.preference.mode === 'system' ? `System · ${currentThemeName}` : currentThemeName,
    );
    // What the row says without opening it: off, or whatever the backup is
    // doing right now, in the same words the page itself uses.
    const backupSummary = $derived(
        !$photoBackupState ? ''
        : !$photoBackupState.settings.enabled ? 'Off'
        : describeBackup($photoBackupState).title,
    );

    function openDetail(next: AccountDetail): void {
        detail = next;
    }

    /**
     * A detail is an in-tab drill-in rather than a modal, but it is still the
     * page's current topmost destination. Claiming the shared sheet stack makes
     * hardware BACK and iOS edge-back leave it before changing tabs, and puts
     * focus back on the row that opened it.
     */
    function closeDetail({ restoreFocus = true }: { restoreFocus?: boolean } = {}): void {
        const previous = detail;
        if (!previous) return;
        detail = null;
        if (!restoreFocus) return;
        // The opener is unmounted with the account list and recreated on the
        // next render, so resolve the bound element after that render instead
        // of trying to focus the now-disconnected old button.
        void tick().then(() => (previous === 'appearance' ? appearanceOpener : backupOpener)?.focus({ preventScroll: true }));
    }

    $effect(() => {
        if (!detail) return;
        const backEntry = pushSheet(() => closeDetail());
        return () => backEntry.release();
    });

    // Account stays mounted behind the tab switcher. A detail screen must not
    // keep a hidden BACK-stack entry after the user chooses another tab.
    $effect(() => {
        if ($activeTab !== 'account' && detail) {
            closeDetail({ restoreFocus: false });
        }
    });

    function openDrive(channel: DriveChannel): void {
        void switchActiveChannel(channel.id);
        activeTab.set('files');
    }

    /**
     * How much of this drive is in use. The desktop reads it off the sidebar
     * footer; the phone had nowhere to read it at all, because the element the
     * file list writes it into is one of the hidden hosts. Asking for the
     * figure here keeps it a fact about the account, on the tab that holds the
     * other ones.
     */
    let storageBytes = $state<number | null>(null);
    let storageError = $state(false);
    // A drive can change while its request is still in flight. The generation
    // makes a late answer inert instead of letting it overwrite the new drive's
    // figure. It is deliberately ordinary state: the effect should depend only
    // on the active tab and drive, never its own request progress.
    let storageGeneration = 0;
    const storageDriveName = $derived($activeDrive?.title || 'Current drive');

    const storageLabel = $derived(
        storageBytes !== null ? formatBytes(storageBytes)
        : storageError ? 'Unavailable'
        : 'Calculating…',
    );

    async function loadStorage(driveId: number | null): Promise<void> {
        const generation = ++storageGeneration;
        storageBytes = null;
        storageError = false;
        try {
            const bytes = await getStorageUsed();
            if (
                generation !== storageGeneration
                || driveId !== $activeDrive?.id
                || $activeTab !== 'account'
            ) return;
            const usable = Number.isFinite(bytes) && bytes >= 0;
            storageBytes = usable ? bytes : null;
            storageError = !usable;
        } catch {
            if (
                generation !== storageGeneration
                || driveId !== $activeDrive?.id
                || $activeTab !== 'account'
            ) return;
            storageBytes = null;
            storageError = true;
        }
    }

    // Read it when the tab is actually being looked at, and again after a drive
    // switch: the figure is per-drive, and a stale one is worse than a late one.
    $effect(() => {
        if ($activeTab !== 'account') return;
        void loadStorage($activeDrive?.id ?? null);
    });

    /**
     * The trash is a view of the drive, not a dialog, so opening it from here
     * means moving to the tab that shows the drive. Entering first means the
     * Files tab is never briefly the folder the user left.
     */
    function showTrash(): void {
        openTrash();
        mobileActiveTab.set('files');
    }
</script>

<div class="mobile-scroll account-tab">
    {#if detail}
        <section class="account-detail" aria-label={DETAIL_TITLES[detail]}>
            <button type="button" class="account-detail-back" onclick={() => closeDetail()}>
                <ChevronLeftIcon size={20} strokeWidth={2.2} aria-hidden="true" />
                {DETAIL_TITLES[detail]}
            </button>
            {#if detail === 'appearance'}
                <AppearancePanel autofocus />
            {:else}
                <PhotoBackupPanel page />
            {/if}
        </section>
    {:else}
    <div class="account-identity">
        <Avatar user={$profileUser} large />
        <div class="account-identity-meta">
            <div class="account-name">{displayName}</div>
            {#if handle}
                <div class="account-handle">@{handle}</div>
            {/if}
        </div>
    </div>

    <section class="account-group" aria-label="Storage">
        <h2 class="mobile-section-label">Storage</h2>
        <div class="account-card">
            <div class="account-row account-row-static">
                <HardDriveIcon class="account-row-icon" size={20} strokeWidth={1.9} aria-hidden="true" />
                <span class="account-row-label">
                    <span>Used in</span>
                    <span class="account-row-detail" title={storageDriveName}>{storageDriveName}</span>
                </span>
                <span class="account-row-value">{storageLabel}</span>
            </div>
            <!-- Deleted items are the other half of "what this drive is
                 holding", so the way back to them sits with the figure that
                 counts them, not in a menu of its own. -->
            <button type="button" class="account-row" onclick={showTrash}>
                <Trash2Icon class="account-row-icon" size={20} strokeWidth={1.9} aria-hidden="true" />
                <span class="account-row-label">Trash</span>
                <ChevronRightIcon class="account-row-chevron" size={18} strokeWidth={2} aria-hidden="true" />
            </button>
            {#if $activeTab === 'account'}<PhotoCachePanel />{/if}
            <button
                bind:this={backupOpener}
                type="button"
                class="account-row"
                onclick={() => openDetail('backup')}
            >
                <CloudUploadIcon class="account-row-icon" size={20} strokeWidth={1.9} aria-hidden="true" />
                <span class="account-row-label">Photo &amp; video backup</span>
                <span class="account-row-value">{backupSummary}</span>
                <ChevronRightIcon class="account-row-chevron" size={18} strokeWidth={2} aria-hidden="true" />
            </button>
        </div>
    </section>

    <section class="account-group" aria-label="Drives">
        <h2 class="mobile-section-label">Drives</h2>
        <div class="account-card">
            {#each drives as channel (channel.id)}
                <button type="button" class="account-row" onclick={() => openDrive(channel)}>
                    <FolderIcon class="account-row-icon" size={20} strokeWidth={1.9} aria-hidden="true" />
                    <span class="account-row-label">{channel.title || 'Untitled'}</span>
                    <span class="account-row-value">{channel.kind === 'shared' ? 'Shared' : 'Personal'}</span>
                    <ChevronRightIcon class="account-row-chevron" size={18} strokeWidth={2} aria-hidden="true" />
                </button>
            {/each}
            <button type="button" class="account-row" onclick={openJoinDriveModal}>
                <Link2Icon class="account-row-icon" size={20} strokeWidth={1.9} aria-hidden="true" />
                <span class="account-row-label">Join with a link</span>
                <ChevronRightIcon class="account-row-chevron" size={18} strokeWidth={2} aria-hidden="true" />
            </button>
            <button type="button" class="account-row" onclick={openNewDriveModal}>
                <FolderPlusIcon class="account-row-icon" size={20} strokeWidth={1.9} aria-hidden="true" />
                <span class="account-row-label">Create a shared drive</span>
                <ChevronRightIcon class="account-row-chevron" size={18} strokeWidth={2} aria-hidden="true" />
            </button>
        </div>
    </section>

    {#if $encryptionEntryVisible}
        <section class="account-group" aria-label="Vault">
            <h2 class="mobile-section-label">Vault</h2>
            <div class="account-card">
                <button type="button" class="account-row" onclick={openEncryptionSettingsModal}>
                    <LockKeyholeIcon class="account-row-icon" size={20} strokeWidth={1.9} aria-hidden="true" />
                    <span class="account-row-label">Encryption settings</span>
                    <ChevronRightIcon class="account-row-chevron" size={18} strokeWidth={2} aria-hidden="true" />
                </button>
            </div>
        </section>
    {/if}

    <section class="account-group" aria-label="Appearance">
        <h2 class="mobile-section-label">Appearance</h2>
        <div class="account-card">
            <button
                bind:this={appearanceOpener}
                type="button"
                class="account-row"
                onclick={() => openDetail('appearance')}
            >
                <PaletteIcon class="account-row-icon" size={20} strokeWidth={1.9} aria-hidden="true" />
                <span class="account-row-label">Appearance</span>
                <span class="account-row-value">{appearanceSummary}</span>
                <ChevronRightIcon class="account-row-chevron" size={18} strokeWidth={2} aria-hidden="true" />
            </button>
        </div>
    </section>

    <section class="account-group" aria-label="About TDrive">
        <h2 class="mobile-section-label">About</h2>
        <div class="account-card account-about">
            <div class="account-about-name">TDrive</div>
            <div class="account-about-tagline">Your Telegram, as a drive.</div>
        </div>
    </section>

    <div class="account-group">
        <button type="button" class="account-logout" onclick={openLogoutModal}>
            <LogOutIcon size={20} strokeWidth={1.9} aria-hidden="true" />
            Log out
        </button>
    </div>
    {/if}
</div>

<style>
    /* A row that only reports something: no tap, so no tap affordance. */
    :global(html.mobile .account-row.account-row-static) {
        cursor: default;
        background: transparent;
    }
</style>
