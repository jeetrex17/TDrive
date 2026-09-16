<script lang="ts">
    import FileUpIcon from '@lucide/svelte/icons/file-up';
    import FolderPlusIcon from '@lucide/svelte/icons/folder-plus';
    import FolderUpIcon from '@lucide/svelte/icons/folder-up';
    import PlusIcon from '@lucide/svelte/icons/plus';
    import UploadIcon from '@lucide/svelte/icons/upload';
    import { tick } from 'svelte';
    import { isMobilePlatform } from '../../api';
    import { pushSheet, type SheetHandle } from '../modals/sheet-stack';

    // Desktop anchors this menu under its toolbar button. The phone cannot: the
    // trigger is docked into the middle of the tab bar, hard against the bottom
    // safe area, so a popover has nowhere to open into and collides with the bar
    // it sits in. On a phone it becomes a bottom sheet instead, which is the
    // same surface the row actions and the drive switcher already use.
    const asSheet = isMobilePlatform();

    interface Props {
        onFiles: () => void;
        // Left out where the platform has no directory picker (Android).
        onFolder?: () => void;
        // Only the phone menu surfaces New folder here; desktop omits it.
        onNewFolder?: () => void;
    }

    let { onFiles, onFolder, onNewFolder }: Props = $props();

    let open = $state(false);
    let buttonEl = $state<HTMLButtonElement | null>(null);
    let menuEl = $state<HTMLElement | null>(null);
    let filesEl = $state<HTMLButtonElement | null>(null);
    let newFolderEl = $state<HTMLButtonElement | null>(null);
    let folderEl = $state<HTMLButtonElement | null>(null);

    async function openMenu(): Promise<void> {
        open = true;
        await tick();
        filesEl?.focus();
    }

    function closeMenu(returnFocus = false): void {
        open = false;
        if (returnFocus) buttonEl?.focus();
    }

    function activate(action: () => void): void {
        closeMenu();
        action();
    }

    function chooseFolder(): void {
        if (onFolder) activate(onFolder);
    }

    function chooseNewFolder(): void {
        if (onNewFolder) activate(onNewFolder);
    }

    function onWindowKeydown(event: KeyboardEvent): void {
        if (event.key === 'Escape' && open) closeMenu(true);
    }

    function onDocumentClick(event: MouseEvent): void {
        if (!open) return;
        const target = event.target as Node;
        if (buttonEl?.contains(target) || menuEl?.contains(target)) return;
        closeMenu();
    }

    // Android BACK is the fourth way out the sheet promises.
    let backEntry: SheetHandle | null = null;
    $effect(() => {
        if (open) {
            backEntry ??= pushSheet(() => closeMenu());
            return;
        }
        backEntry?.release();
        backEntry = null;
    });

    // Arrow-key navigation between the menu items.
    function onMenuKeydown(event: KeyboardEvent): void {
        if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
        event.preventDefault();
        const items = [filesEl, newFolderEl, folderEl].filter(Boolean) as HTMLElement[];
        if (!items.length) return;
        const idx = items.indexOf(document.activeElement as HTMLElement);
        const next = event.key === 'ArrowDown' ? (idx + 1) % items.length : (idx - 1 + items.length) % items.length;
        items[next].focus();
    }
</script>

<svelte:window onkeydown={onWindowKeydown} />
<svelte:document onclick={onDocumentClick} />

<button
    bind:this={buttonEl}
    id="upload-btn"
    class="primary-btn upload-btn"
    type="button"
    aria-haspopup="menu"
    aria-expanded={open ? 'true' : 'false'}
    aria-controls="upload-menu"
    onclick={(event) => {
        event.stopPropagation();
        if (open) closeMenu();
        else void openMenu();
    }}
>
    <!-- On a phone this is one cell of the tab bar, so it is built like one:
         a glyph over a label, with the same pill the active tab wears, filled
         rather than tinted because this one acts instead of navigating. The
         menu behind it creates a folder as readily as it uploads a file, so a
         plus says what the button does. Beside the word Upload on a desktop the
         tray reads as the verb it accompanies. -->
    {#if isMobilePlatform()}
        <span class="upload-btn-pill" aria-hidden="true">
            <PlusIcon class="btn-icon" size={24} strokeWidth={2.2} />
        </span>
    {:else}
        <UploadIcon class="btn-icon" size={16} strokeWidth={2} aria-hidden="true" />
    {/if}
    Upload
</button>
{#if asSheet && open}
    <!-- Dimming the screen is what makes the sheet read as a layer rather than
         a box floating over the bar. It is a real element, not a pseudo, so a
         tap on it counts as outside the menu and closes it. -->
    <div class="upload-scrim" aria-hidden="true" onclick={() => closeMenu()}></div>
{/if}
<div
    bind:this={menuEl}
    id="upload-menu"
    class="upload-menu"
    class:is-sheet={asSheet}
    role="menu"
    tabindex="-1"
    style={`display: ${open ? 'flex' : 'none'};`}
    onkeydown={onMenuKeydown}
>
    {#if asSheet}
        <div class="sheet-handle" aria-hidden="true"><span></span></div>
    {/if}
    <button bind:this={filesEl} id="upload-menu-files" class="upload-menu-item" type="button" role="menuitem" onclick={() => activate(onFiles)}>
        <FileUpIcon size={18} strokeWidth={1.8} aria-hidden="true" />
        {onNewFolder ? 'Upload files' : 'Files'}
    </button>
    {#if onNewFolder}
        <button bind:this={newFolderEl} id="upload-menu-new-folder" class="upload-menu-item" type="button" role="menuitem" onclick={chooseNewFolder}>
            <FolderPlusIcon size={18} strokeWidth={1.8} aria-hidden="true" />
            New folder
        </button>
    {/if}
    {#if onFolder}
        <button bind:this={folderEl} id="upload-menu-folder" class="upload-menu-item" type="button" role="menuitem" onclick={chooseFolder}>
            <FolderUpIcon size={18} strokeWidth={1.8} aria-hidden="true" />
            {onNewFolder ? 'Upload folder' : 'Folder'}
        </button>
    {/if}
    <!-- No Cancel row. The sheet already has three ways out that cost less than
         reading a fourth option: the scrim, a downward swipe, and Android's
         back. A Cancel button in a sheet this short mostly adds a line of text
         that has to be read and dismissed as not-what-you-want. -->
</div>
