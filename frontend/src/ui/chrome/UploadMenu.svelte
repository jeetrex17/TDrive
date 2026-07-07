<script lang="ts">
    import { tick } from 'svelte';

    interface Props {
        onFiles: () => void;
        onFolder: () => void;
    }

    let { onFiles, onFolder }: Props = $props();

    let open = $state(false);
    let buttonEl = $state<HTMLButtonElement | null>(null);
    let menuEl = $state<HTMLElement | null>(null);
    let filesEl = $state<HTMLButtonElement | null>(null);
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

    function onWindowKeydown(event: KeyboardEvent): void {
        if (event.key === 'Escape' && open) closeMenu(true);
    }

    function onDocumentClick(event: MouseEvent): void {
        if (!open) return;
        const target = event.target as Node;
        if (buttonEl?.contains(target) || menuEl?.contains(target)) return;
        closeMenu();
    }

    // Arrow-key navigation between the two menu items.
    function onMenuKeydown(event: KeyboardEvent): void {
        if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
        event.preventDefault();
        const items = [filesEl, folderEl].filter(Boolean) as HTMLElement[];
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
    <svg class="btn-icon" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4"></path></svg>
    Upload
</button>
<div
    bind:this={menuEl}
    id="upload-menu"
    class="upload-menu"
    role="menu"
    tabindex="-1"
    style={`display: ${open ? 'flex' : 'none'};`}
    onkeydown={onMenuKeydown}
>
    <button bind:this={filesEl} id="upload-menu-files" class="upload-menu-item" type="button" role="menuitem" onclick={() => activate(onFiles)}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path stroke-linecap="round" stroke-linejoin="round" d="M9 13h6m-6 4h6m2 4H7a2 2 0 01-2-2V5a2 2 0 012-2h7l5 5v11a2 2 0 01-2 2z"/></svg>
        Files
    </button>
    <button bind:this={folderEl} id="upload-menu-folder" class="upload-menu-item" type="button" role="menuitem" onclick={() => activate(onFolder)}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path stroke-linecap="round" stroke-linejoin="round" d="M3 7a2 2 0 012-2h5l2 2h7a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2V7z"/></svg>
        Folder
    </button>
</div>
