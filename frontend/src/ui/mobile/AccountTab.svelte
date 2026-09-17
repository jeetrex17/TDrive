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
    import Avatar from '../chrome/Avatar.svelte';
    import { getStorageUsed } from '../../api';
    import { formatBytes } from '../../utils';
    import { encryptionEntryVisible, profileLoaded, profileUser } from '../chrome/profile-store';
    import { sidebarState } from '../sidebar/sidebar-store';
    import AppearancePanel from '../theme/AppearancePanel.svelte';
    import { getThemeDefinition } from '../theme/theme-model';
    import { themeState } from '../theme/theme-controller';
    import { switchActiveChannel } from '../../modules/channels';
    import { openEncryptionSettingsModal } from '../../modules/modals/encryption-settings';
    import { openJoinDriveModal } from '../../modules/modals/join-drive';
    import { openNewDriveModal } from '../../modules/modals/new-drive';
    import { openLogoutModal } from '../../modules/modals/logout';
    import type { DriveChannel } from '../../types';
    import { activeDrive, activeTab } from './mobile-shell-store';
    import { pushSheet } from '../modals/sheet-stack';

    const displayName = $derived(
        !$profileLoaded ? 'Loading account' : $profileUser?.displayName || 'Telegram account',
    );
    const handle = $derived(($profileUser?.username || '').trim());
    const drives = $derived([...$sidebarState.personal, ...$sidebarState.shared]);
    let appearanceOpen = $state(false);
    let appearanceOpener = $state<HTMLButtonElement | null>(null);
    const currentThemeName = $derived(getThemeDefinition($themeState.resolvedThemeId).name);
    const appearanceSummary = $derived(
        $themeState.preference.mode === 'system' ? `System · ${currentThemeName}` : currentThemeName,
    );

    function openAppearance(): void {
        appearanceOpen = true;
    }

    /**
     * Appearance is an in-tab drill-in rather than a modal, but it is still
     * the page's current topmost destination. Claiming the shared sheet stack
     * makes hardware BACK and iOS edge-back leave this detail before changing
     * tabs, and puts focus back on the row that opened it.
     */
    function closeAppearance({ restoreFocus = true }: { restoreFocus?: boolean } = {}): void {
        if (!appearanceOpen) return;
        appearanceOpen = false;
        if (!restoreFocus) return;
        // The opener is unmounted with the account list and recreated on the
        // next render, so resolve the bound element after that render instead
        // of trying to focus the now-disconnected old button.
        void tick().then(() => appearanceOpener?.focus({ preventScroll: true }));
    }

    $effect(() => {
        if (!appearanceOpen) return;
        const backEntry = pushSheet(() => closeAppearance());
        return () => backEntry.release();
    });

    // Account stays mounted behind the tab switcher. A detail screen must not
    // keep a hidden BACK-stack entry after the user chooses another tab.
    $effect(() => {
        if ($activeTab !== 'account' && appearanceOpen) {
            closeAppearance({ restoreFocus: false });
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
</script>

<div class="mobile-scroll account-tab">
    {#if appearanceOpen}
        <section class="account-appearance-detail" aria-label="Appearance settings">
            <button type="button" class="account-appearance-back" onclick={() => closeAppearance()}>
                <ChevronLeftIcon size={20} strokeWidth={2.2} aria-hidden="true" />
                Appearance
            </button>
            <AppearancePanel autofocus />
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
                onclick={openAppearance}
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
