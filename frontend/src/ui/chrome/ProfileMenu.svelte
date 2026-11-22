<script lang="ts">
    import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
    import DownloadIcon from '@lucide/svelte/icons/download';
    import LogOutIcon from '@lucide/svelte/icons/log-out';
    import SettingsIcon from '@lucide/svelte/icons/settings';
    import { tick } from 'svelte';
    import { isApplePlatform } from '../../utils';
    import { eventOccurredWithin } from '../event-path';
    import { openSettings } from '../settings/settings-store';
    import { recoverThemeTransitionClick } from '../theme/theme-interaction';
    import { updateBadge, updatesPanelRequest } from '../updates/update-store';
    import Avatar from './Avatar.svelte';
    import { profileLoaded, profileUser } from './profile-store';

    interface Props {
        // Called when the menu opens, so the module can lazily fetch Me().
        onOpen: () => void;
        onLogout: () => void;
        // False on phones, which update through their app stores.
        updaterAvailable?: boolean;
    }

    let { onOpen, onLogout, updaterAvailable = true }: Props = $props();

    let open = $state(false);
    let triggerEl = $state<HTMLButtonElement | null>(null);
    let menuEl = $state<HTMLElement | null>(null);

    const displayName = $derived(
        !$profileLoaded ? 'Loading account…' : $profileUser?.displayName || 'Telegram account',
    );
    const handle = $derived(($profileUser?.username || '').trim());
    const settingsShortcut = isApplePlatform() ? '⌘,' : 'Ctrl+,';

    // A request nonce (macOS "Check for Updates…", login-screen link) opens
    // Settings on its Updates section. Guarded so it only reacts to changes.
    let lastPanelRequest = 0;
    $effect(() => {
        if (!updaterAvailable) return;
        const nonce = $updatesPanelRequest;
        if (nonce === lastPanelRequest) return;
        lastPanelRequest = nonce;
        onOpen();
        closeMenu();
        openSettings('updates');
    });

    function menuItems(): HTMLElement[] {
        if (!menuEl) return [];
        return Array.from(menuEl.querySelectorAll<HTMLElement>('[role="menuitem"]'))
            .filter((item) => !item.hidden && !item.hasAttribute('disabled'));
    }

    async function openMenu(): Promise<void> {
        open = true;
        onOpen();
        await tick();
        menuItems()[0]?.focus();
    }

    function closeMenu(returnFocus = false): void {
        open = false;
        if (returnFocus) triggerEl?.focus();
    }

    function toggleMenu(): void {
        if (open) closeMenu();
        else void openMenu();
    }

    function activate(action: () => void): void {
        closeMenu();
        action();
    }

    function onMenuKeydown(event: KeyboardEvent): void {
        if (!open) return;
        if (event.key === 'Escape') {
            event.preventDefault();
            event.stopPropagation();
            closeMenu(true);
            return;
        }
        if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;

        const items = menuItems();
        if (!items.length) return;

        event.preventDefault();
        const current = document.activeElement as HTMLElement | null;
        const index = current ? items.indexOf(current) : -1;

        if (event.key === 'Home') {
            items[0].focus();
            return;
        }
        if (event.key === 'End') {
            items[items.length - 1].focus();
            return;
        }

        const direction = event.key === 'ArrowDown' ? 1 : -1;
        const next = index >= 0
            ? (index + direction + items.length) % items.length
            : direction > 0 ? 0 : items.length - 1;
        items[next].focus();
    }

    function onWindowKeydown(event: KeyboardEvent): void {
        if (event.key === 'Escape' && open) {
            closeMenu(true);
        }
    }

    function onDocumentClick(event: MouseEvent): void {
        if (!open) return;
        if (eventOccurredWithin(event, menuEl) || eventOccurredWithin(event, triggerEl)) return;
        if (recoverThemeTransitionClick(event, menuEl)
            || recoverThemeTransitionClick(event, triggerEl)) return;
        closeMenu();
    }
</script>

<svelte:window onkeydown={onWindowKeydown} />
<svelte:document onclickcapture={onDocumentClick} />

<button
    bind:this={triggerEl}
    id="profile-trigger"
    data-theme-hit-target
    class="profile-trigger"
    type="button"
    aria-haspopup="menu"
    aria-expanded={open ? 'true' : 'false'}
    aria-controls="profile-menu"
    title="Account"
    onclick={(event) => {
        event.stopPropagation();
        toggleMenu();
    }}
>
    <Avatar user={$profileUser} />
    {#if $updateBadge === 'ready'}
        <span class="profile-update-dot" aria-hidden="true"></span>
    {/if}
    <ChevronDownIcon class="profile-chevron" size={16} strokeWidth={2} aria-hidden="true" />
</button>

<div
    bind:this={menuEl}
    id="profile-menu"
    class="profile-menu"
    role="menu"
    aria-labelledby="profile-trigger"
    tabindex="-1"
    hidden={!open}
    onkeydown={onMenuKeydown}
>
    <div class="profile-menu-header">
        <Avatar user={$profileUser} large />
        <div class="profile-menu-meta">
            <div id="profile-menu-name" class="profile-menu-name">{displayName}</div>
            {#if handle}
                <div id="profile-menu-handle" class="profile-menu-handle">@{handle}</div>
            {/if}
        </div>
    </div>
    <div class="profile-menu-divider" role="separator"></div>
    <button
        id="profile-menu-settings"
        class="profile-menu-item"
        type="button"
        role="menuitem"
        aria-keyshortcuts={isApplePlatform() ? 'Meta+Comma' : 'Control+Comma'}
        onclick={() => activate(() => openSettings())}
    >
        <SettingsIcon size={16} strokeWidth={2} aria-hidden="true" />
        <span>Settings…</span>
        <span class="profile-menu-value" aria-hidden="true">{settingsShortcut}</span>
    </button>
    {#if updaterAvailable}
        <button
            id="profile-menu-updates"
            class="profile-menu-item"
            type="button"
            role="menuitem"
            onclick={() => activate(() => openSettings('updates'))}
        >
            <DownloadIcon size={16} strokeWidth={2} aria-hidden="true" />
            <span>Check for updates</span>
            {#if $updateBadge === 'ready'}
                <span class="profile-menu-tag" aria-label="Update ready">Ready</span>
            {/if}
        </button>
    {/if}
    <div class="profile-menu-divider" role="separator"></div>
    <button
        id="profile-menu-logout"
        class="profile-menu-item danger-text"
        type="button"
        role="menuitem"
        onclick={() => activate(onLogout)}
    >
        <LogOutIcon size={16} strokeWidth={2} aria-hidden="true" />
        <span>Log out</span>
    </button>
</div>
