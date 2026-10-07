<script lang="ts">
    import FileUpIcon from '@lucide/svelte/icons/file-up';
    import FolderPlusIcon from '@lucide/svelte/icons/folder-plus';
    import FolderUpIcon from '@lucide/svelte/icons/folder-up';
    import PlusIcon from '@lucide/svelte/icons/plus';
    import XIcon from '@lucide/svelte/icons/x';
    import { tick } from 'svelte';
    import { isMobilePlatform } from '../../api';
    import { prefersReducedMotion } from '../mobile/motion';
    import { installModalA11y } from '../modals/modal-a11y';
    import { createSheetDragController } from '../modals/sheet-gesture';

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
    let overlayEl = $state<HTMLElement | null>(null);
    let menuEl = $state<HTMLElement | null>(null);
    let filesEl = $state<HTMLButtonElement | null>(null);
    let newFolderEl = $state<HTMLButtonElement | null>(null);
    let folderEl = $state<HTMLButtonElement | null>(null);

    async function openMenu(): Promise<void> {
        // Touch activation does not consistently move DOM focus. Establish the
        // trigger as the return point before the sheet claims focus.
        buttonEl?.focus({ preventScroll: true });
        open = true;
        await tick();
        if (!asSheet) filesEl?.focus();
    }

    function closeMenu(returnFocus = false): void {
        open = false;
        if (returnFocus && !asSheet) buttonEl?.focus();
    }

    function activate(action: () => void): void {
        closeMenu(false);
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
        if (buttonEl?.contains(target) || overlayEl?.contains(target)) return;
        closeMenu(true);
    }

    // A phone sheet is a modal action surface, not a popover menu. Sharing the
    // modal owner gives it the same inert background, Tab loop, focus return,
    // Escape, and Android BACK contract as every other sheet in the app.
    $effect(() => {
        if (!asSheet || !open || !overlayEl) return;
        const a11y = installModalA11y(overlayEl, {
            requestClose: () => closeMenu(true),
            initialFocus: () => filesEl,
            restoreFocus: () => buttonEl,
        });
        a11y.activate();
        return () => a11y.deactivate();
    });

    // The downward swipe the sheet's grip promises, on the shared physics every
    // other sheet uses. This host only says how to settle and dismiss.
    const sheetDrag = createSheetDragController({
        sheet: () => menuEl,
        dismiss: () => {
            if (menuEl) {
                menuEl.style.animation = '';
                menuEl.style.transform = '';
            }
            closeMenu(true);
        },
        settle: () => {
            if (!menuEl) return;
            menuEl.style.animation = '';
            // A pull that did not reach the line slides back rather than
            // snapping, so the sheet reads as an object the finger let go of.
            menuEl.style.transition = prefersReducedMotion()
                ? 'none'
                : 'transform var(--motion-med) var(--ease-standard)';
            menuEl.style.transform = '';
        },
    });

    // Arrow-key navigation between the menu items, in their visual order.
    function onMenuKeydown(event: KeyboardEvent): void {
        // The desktop popover is not a modal, so Tab should leave it rather than
        // tab through hidden items behind the user. Close and let focus move on
        // from the trigger. The phone sheet loops Tab through its modal owner.
        if (event.key === 'Tab' && !asSheet) {
            closeMenu(false);
            buttonEl?.focus();
            return;
        }
        if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
        event.preventDefault();
        const items = [filesEl, folderEl, newFolderEl].filter(Boolean) as HTMLElement[];
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
    aria-haspopup={asSheet ? 'dialog' : 'menu'}
    aria-expanded={open ? 'true' : 'false'}
    aria-controls="upload-menu"
    onclick={(event) => {
        event.stopPropagation();
        if (open) closeMenu(true);
        else void openMenu();
    }}
>
    <!-- On a phone this is one cell of the tab bar, so it is built like one:
         a glyph over a label, with the same pill the active tab wears, filled
         rather than tinted because this one acts instead of navigating. The
         menu behind it creates a folder as readily as it uploads a file, so a
         plus and the word "New" cover both without naming only one of them. -->
    {#if isMobilePlatform()}
        <span class="upload-btn-pill" aria-hidden="true">
            <PlusIcon class="btn-icon" size={24} strokeWidth={2.2} />
        </span>
    {:else}
        <PlusIcon class="btn-icon" size={16} strokeWidth={2} aria-hidden="true" />
    {/if}
    New
</button>
<div
    bind:this={overlayEl}
    class="upload-menu-overlay"
    class:is-sheet-overlay={asSheet}
>
    {#if asSheet && open}
        <!-- Dimming the screen is what makes the sheet read as a layer rather
             than a box floating over the bar. It lives inside the modal owner
             so it remains tappable while the app behind is inert. -->
        <div class="upload-scrim" aria-hidden="true" onclick={() => closeMenu(true)}></div>
    {/if}
    <div
        bind:this={menuEl}
        id="upload-menu"
        class="upload-menu"
        class:is-sheet={asSheet}
        role={asSheet ? 'dialog' : 'menu'}
        aria-modal={asSheet ? 'true' : undefined}
        aria-label={asSheet ? 'Upload options' : undefined}
        tabindex="-1"
        style={`display: ${open ? 'flex' : 'none'};`}
        onkeydown={onMenuKeydown}
    >
    {#if asSheet}
        <div
            class="sheet-grab"
            role="presentation"
            onpointerdown={sheetDrag.onPointerDown}
            onpointermove={sheetDrag.onPointerMove}
            onpointerup={sheetDrag.onPointerUp}
            onpointercancel={sheetDrag.onPointerUp}
        >
            <div class="sheet-handle" aria-hidden="true"><span></span></div>
        </div>
        <!-- A visible, labelled way out. The scrim and swipe dismiss too, but
             both are hidden from assistive technology. -->
        <button type="button" class="upload-sheet-close" aria-label="Close" onclick={() => closeMenu(true)}>
            <XIcon size={20} strokeWidth={2} aria-hidden="true" />
        </button>
    {/if}
    <button bind:this={filesEl} id="upload-menu-files" class="upload-menu-item" type="button" role={asSheet ? undefined : 'menuitem'} onclick={() => activate(onFiles)}>
        <FileUpIcon size={18} strokeWidth={1.8} aria-hidden="true" />
        {onNewFolder ? 'Upload files' : 'Files'}
    </button>
    {#if onFolder}
        <button bind:this={folderEl} id="upload-menu-folder" class="upload-menu-item" type="button" role={asSheet ? undefined : 'menuitem'} onclick={chooseFolder}>
            <FolderUpIcon size={18} strokeWidth={1.8} aria-hidden="true" />
            {onNewFolder ? 'Upload folder' : 'Folder'}
        </button>
    {/if}
    {#if onNewFolder}
        <!-- Creating a folder is a different kind of action from uploading, so it
             sits below a separator rather than in the same group. -->
        <div class="upload-menu-sep" role="separator"></div>
        <button bind:this={newFolderEl} id="upload-menu-new-folder" class="upload-menu-item" type="button" role={asSheet ? undefined : 'menuitem'} onclick={chooseNewFolder}>
            <FolderPlusIcon size={18} strokeWidth={1.8} aria-hidden="true" />
            New folder
        </button>
    {/if}
    <!-- No Cancel row. The sheet already has four ways out that cost less than
         reading a fifth option: the Close button above, the scrim, a downward
         swipe, and Android's back. -->
    </div>
</div>
