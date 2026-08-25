<script lang="ts">
    import { tick } from 'svelte';
    import { listMountableDrives } from '../../api';
    import MountControl from '../mount/MountControl.svelte';
    import Avatar from './Avatar.svelte';
    import { encryptionEntryVisible, profileLoaded, profileUser } from './profile-store';

    interface Props {
        // Called when the menu opens, so the module can lazily fetch Me().
        onOpen: () => void;
        onEncryptionSettings: () => void;
        onLogout: () => void;
    }

    let { onOpen, onEncryptionSettings, onLogout }: Props = $props();

    let open = $state(false);
    let triggerEl = $state<HTMLButtonElement | null>(null);
    let menuEl = $state<HTMLElement | null>(null);

    const displayName = $derived(
        !$profileLoaded ? 'Loading account…' : $profileUser?.display_name || 'Telegram account',
    );
    const handle = $derived(($profileUser?.username || '').trim());

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
        const target = event.target as Node;
        if (menuEl?.contains(target) || triggerEl?.contains(target)) return;
        closeMenu();
    }
</script>

<svelte:window onkeydown={onWindowKeydown} />
<svelte:document onclickcapture={onDocumentClick} />

<button
    bind:this={triggerEl}
    id="profile-trigger"
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
    <svg class="profile-chevron" aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M6 9l6 6 6-6" /></svg>
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
    {#if $encryptionEntryVisible}
        <button
            id="profile-menu-encryption-settings"
            class="profile-menu-item"
            type="button"
            role="menuitem"
            onclick={() => activate(onEncryptionSettings)}
        >
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><rect x="5" y="11" width="14" height="10" rx="2"/><path stroke-linecap="round" stroke-linejoin="round" d="M8 11V7a4 4 0 018 0v4"/></svg>
            <span>Encryption settings</span>
        </button>
    {/if}
    <MountControl
        loadDrives={listMountableDrives}
        onMenuAction={() => closeMenu()}
    />
    <div class="profile-menu-divider" role="separator"></div>
    <button
        id="profile-menu-logout"
        class="profile-menu-item danger-text"
        type="button"
        role="menuitem"
        onclick={() => activate(onLogout)}
    >
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path stroke-linecap="round" stroke-linejoin="round" d="M17 16l4-4m0 0l-4-4m4 4H7m6 4v1a3 3 0 01-3 3H6a3 3 0 01-3-3V7a3 3 0 013-3h4a3 3 0 013 3v1"/></svg>
        <span>Log out</span>
    </button>
</div>
