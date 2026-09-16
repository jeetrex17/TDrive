<script lang="ts">
    import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
    import FolderIcon from '@lucide/svelte/icons/folder';
    import FolderPlusIcon from '@lucide/svelte/icons/folder-plus';
    import HardDriveIcon from '@lucide/svelte/icons/hard-drive';
    import Link2Icon from '@lucide/svelte/icons/link-2';
    import LockKeyholeIcon from '@lucide/svelte/icons/lock-keyhole';
    import LogOutIcon from '@lucide/svelte/icons/log-out';
    import Avatar from '../chrome/Avatar.svelte';
    import { getStorageUsed } from '../../api';
    import { formatBytes } from '../../utils';
    import { encryptionEntryVisible, profileLoaded, profileUser } from '../chrome/profile-store';
    import { sidebarState } from '../sidebar/sidebar-store';
    import AppearancePanel from '../theme/AppearancePanel.svelte';
    import { switchActiveChannel } from '../../modules/channels';
    import { openEncryptionSettingsModal } from '../../modules/modals/encryption-settings';
    import { openJoinDriveModal } from '../../modules/modals/join-drive';
    import { openNewDriveModal } from '../../modules/modals/new-drive';
    import { openLogoutModal } from '../../modules/modals/logout';
    import type { DriveChannel } from '../../types';
    import { activeDrive, activeTab } from './mobile-shell-store';

    const displayName = $derived(
        !$profileLoaded ? 'Loading account' : $profileUser?.displayName || 'Telegram account',
    );
    const handle = $derived(($profileUser?.username || '').trim());
    const drives = $derived([...$sidebarState.personal, ...$sidebarState.shared]);

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
    // Deliberately not $state: the effect below reads it, and a reactive read
    // would make the effect depend on its own progress.
    let storageLoading = false;

    const storageLabel = $derived(
        storageBytes !== null ? formatBytes(storageBytes)
        : storageError ? 'Unavailable'
        : 'Calculating…',
    );

    async function loadStorage(): Promise<void> {
        if (storageLoading) return;
        storageLoading = true;
        try {
            const bytes = await getStorageUsed();
            const usable = Number.isFinite(bytes) && bytes >= 0;
            storageBytes = usable ? bytes : null;
            storageError = !usable;
        } catch {
            storageBytes = null;
            storageError = true;
        } finally {
            storageLoading = false;
        }
    }

    // Read it when the tab is actually being looked at, and again after a drive
    // switch: the figure is per-drive, and a stale one is worse than a late one.
    $effect(() => {
        if ($activeTab !== 'account') return;
        void $activeDrive?.id;
        void loadStorage();
    });
</script>

<div class="mobile-scroll account-tab">
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
                <span class="account-row-label">Used in this drive</span>
                <span class="account-row-value">{storageLabel}</span>
            </div>
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
        <AppearancePanel />
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
</div>

<style>
    /* A row that only reports something: no tap, so no tap affordance. */
    :global(html.mobile .account-row.account-row-static) {
        cursor: default;
        background: transparent;
    }
</style>
