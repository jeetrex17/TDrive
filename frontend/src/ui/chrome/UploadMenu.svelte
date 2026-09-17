<script lang="ts">
    import FileUpIcon from '@lucide/svelte/icons/file-up';
    import FolderPlusIcon from '@lucide/svelte/icons/folder-plus';
    import FolderUpIcon from '@lucide/svelte/icons/folder-up';
    import PlusIcon from '@lucide/svelte/icons/plus';
    import UploadIcon from '@lucide/svelte/icons/upload';
    import { tick } from 'svelte';
    import { isMobilePlatform } from '../../api';
    import { prefersReducedMotion } from '../mobile/motion';
    import { installModalA11y } from '../modals/modal-a11y';
    import { createSheetDrag, sheetOffset, shouldDismiss } from '../modals/sheet-gesture';

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

    // The downward swipe the sheet's grip promises, on the same physics the
    // other sheets use. Without it the mark was decoration: the one gesture
    // every phone user tries on a sheet did nothing at all.
    let dragStartY = 0;
    let dragDelta = 0;
    let dragging = false;
    const drag = createSheetDrag();

    function onHandlePointerDown(event: PointerEvent): void {
        if (!menuEl) return;
        dragging = true;
        dragStartY = event.clientY;
        dragDelta = 0;
        drag.start(event);
        // Neither the entrance nor a previous spring-back may ease the sheet
        // while a finger is on it: it tracks the thumb 1:1 or not at all.
        menuEl.style.animation = 'none';
        menuEl.style.transition = 'none';
        (event.currentTarget as HTMLElement).setPointerCapture?.(event.pointerId);
    }

    function onHandlePointerMove(event: PointerEvent): void {
        if (!dragging || !menuEl) return;
        dragDelta = sheetOffset(event.clientY - dragStartY, menuEl.offsetHeight);
        drag.track(event);
        menuEl.style.transform = `translateY(${dragDelta}px)`;
    }

    function onHandlePointerUp(): void {
        if (!dragging || !menuEl) return;
        dragging = false;
        const sheet = menuEl;
        const threshold = Math.max(88, sheet.offsetHeight * 0.28);
        const dismissed = shouldDismiss(dragDelta, drag.velocity(), threshold);
        dragDelta = 0;
        drag.reset();
        sheet.style.animation = '';
        if (dismissed) {
            sheet.style.transform = '';
            closeMenu(true);
            return;
        }
        // A pull that did not reach the line slides back rather than snapping,
        // so the sheet reads as an object the finger let go of.
        sheet.style.transition = prefersReducedMotion()
            ? 'none'
            : 'transform var(--motion-med) var(--ease-standard)';
        sheet.style.transform = '';
    }

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
            class="sheet-handle"
            aria-hidden="true"
            onpointerdown={onHandlePointerDown}
            onpointermove={onHandlePointerMove}
            onpointerup={onHandlePointerUp}
            onpointercancel={onHandlePointerUp}
        ><span></span></div>
    {/if}
    <button bind:this={filesEl} id="upload-menu-files" class="upload-menu-item" type="button" role={asSheet ? undefined : 'menuitem'} onclick={() => activate(onFiles)}>
        <FileUpIcon size={18} strokeWidth={1.8} aria-hidden="true" />
        {onNewFolder ? 'Upload files' : 'Files'}
    </button>
    {#if onNewFolder}
        <button bind:this={newFolderEl} id="upload-menu-new-folder" class="upload-menu-item" type="button" role={asSheet ? undefined : 'menuitem'} onclick={chooseNewFolder}>
            <FolderPlusIcon size={18} strokeWidth={1.8} aria-hidden="true" />
            New folder
        </button>
    {/if}
    {#if onFolder}
        <button bind:this={folderEl} id="upload-menu-folder" class="upload-menu-item" type="button" role={asSheet ? undefined : 'menuitem'} onclick={chooseFolder}>
            <FolderUpIcon size={18} strokeWidth={1.8} aria-hidden="true" />
            {onNewFolder ? 'Upload folder' : 'Folder'}
        </button>
    {/if}
    <!-- No Cancel row. The sheet already has three ways out that cost less than
         reading a fourth option: the scrim, a downward swipe, and Android's
         back. A Cancel button in a sheet this short mostly adds a line of text
         that has to be read and dismissed as not-what-you-want. -->
    </div>
</div>
