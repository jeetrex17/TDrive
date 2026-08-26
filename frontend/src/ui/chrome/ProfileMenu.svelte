<script lang="ts">
    import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
    import LockKeyholeIcon from '@lucide/svelte/icons/lock-keyhole';
    import LogOutIcon from '@lucide/svelte/icons/log-out';
    import PaletteIcon from '@lucide/svelte/icons/palette';
    import { tick } from 'svelte';
    import { eventOccurredWithin } from '../event-path';
    import AppearancePanel from '../theme/AppearancePanel.svelte';
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
    let view = $state<'account' | 'appearance'>('account');
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
        view = 'account';
        open = true;
        onOpen();
        await tick();
        menuItems()[0]?.focus();
    }

    function closeMenu(returnFocus = false): void {
        open = false;
        view = 'account';
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

    async function openAppearance(): Promise<void> {
        view = 'appearance';
        await tick();
        menuEl?.querySelector<HTMLElement>('[data-appearance-mode][aria-checked="true"]')?.focus();
    }

    async function closeAppearance(): Promise<void> {
        view = 'account';
        await tick();
        menuEl?.querySelector<HTMLElement>('#profile-menu-appearance')?.focus();
    }

    function onMenuKeydown(event: KeyboardEvent): void {
        if (!open) return;
        if (event.key === 'Escape') {
            event.preventDefault();
            // Keep the window-level Escape handler from also closing the
            // popover after this view has handled the first navigation step.
            event.stopPropagation();
            if (view === 'appearance') {
                void closeAppearance();
                return;
            }
            closeMenu(true);
            return;
        }
        if (view !== 'account') return;
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
    <ChevronDownIcon class="profile-chevron" size={16} strokeWidth={2} aria-hidden="true" />
</button>

<div
    bind:this={menuEl}
    id="profile-menu"
    class:appearance-view={view === 'appearance'}
    class="profile-menu"
    role={view === 'account' ? 'menu' : 'dialog'}
    aria-labelledby={view === 'account' ? 'profile-trigger' : 'appearance-title'}
    tabindex="-1"
    hidden={!open}
    onkeydown={onMenuKeydown}
>
    {#if view === 'appearance'}
        <AppearancePanel />
    {:else}
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
            id="profile-menu-appearance"
            class="profile-menu-item"
            type="button"
            role="menuitem"
            onclick={() => void openAppearance()}
        >
            <PaletteIcon size={20} strokeWidth={2} aria-hidden="true" />
            <span>Appearance</span>
        </button>
        {#if $encryptionEntryVisible}
            <button
                id="profile-menu-encryption-settings"
                class="profile-menu-item"
                type="button"
                role="menuitem"
                onclick={() => activate(onEncryptionSettings)}
            >
                <LockKeyholeIcon size={20} strokeWidth={2} aria-hidden="true" />
                <span>Encryption settings</span>
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
            <LogOutIcon size={20} strokeWidth={2} aria-hidden="true" />
            <span>Log out</span>
        </button>
    {/if}
</div>
