<script lang="ts">
    import FolderPlusIcon from '@lucide/svelte/icons/folder-plus';
    import Link2Icon from '@lucide/svelte/icons/link-2';
    import XIcon from '@lucide/svelte/icons/x';
    import {
        handleDriveClick,
        handlePendingClick,
        showPendingActionsMenu,
        showSharedActionsMenu,
    } from '../../modules/sidebar';
    import { createSheetDrag, sheetOffset, shouldDismiss } from '../modals/sheet-gesture';
    import DriveList from '../sidebar/DriveList.svelte';
    import { driveSwitcherOpen, closeDriveSwitcher } from './mobile-shell-store';

    let sheetEl = $state<HTMLElement | null>(null);
    let scrimEl = $state<HTMLElement | null>(null);

    const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])';

    function focusables(): HTMLElement[] {
        return Array.from(sheetEl?.querySelectorAll<HTMLElement>(FOCUSABLE) ?? [])
            .filter((element) => !element.closest('[inert], [hidden]'));
    }

    // While the sheet is up it is the only thing on screen, so the shell behind
    // it stops answering: without this a screen reader walks straight out of the
    // sheet into the file list it is covering. Only the shell, though -- the
    // overlay layer above it carries the drive row's own action menu, and that
    // has to stay reachable with the sheet still open.
    let hiddenBehind: HTMLElement[] = [];

    function hideBehind(): void {
        const shell = sheetEl?.parentElement;
        if (!shell) return;
        hiddenBehind = Array.from(shell.children).filter(
            (child): child is HTMLElement =>
                child instanceof HTMLElement && child !== sheetEl && child !== scrimEl && !child.inert,
        );
        for (const element of hiddenBehind) element.inert = true;
    }

    function showBehind(): void {
        for (const element of hiddenBehind) element.inert = false;
        hiddenBehind = [];
    }

    // Focus into the sheet when it opens so keyboard and screen-reader users land
    // inside it, not behind it, and hand it back to whatever opened it on the way
    // out: the sheet goes inert while closing, so focus left inside it lands on
    // the document body and the reader starts again from the top.
    let opener: HTMLElement | null = null;
    $effect(() => {
        if (!$driveSwitcherOpen || !sheetEl) return;
        opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
        hideBehind();
        (sheetEl.querySelector<HTMLElement>('.drive-item, #open-join-drive') ?? sheetEl)
            .focus({ preventScroll: true });

        return () => {
            showBehind();
            const back = opener?.isConnected && opener !== document.body
                ? opener
                : document.querySelector<HTMLElement>('.drive-header-btn');
            opener = null;
            back?.focus({ preventScroll: true });
        };
    });

    function onKeydown(event: KeyboardEvent): void {
        if (!$driveSwitcherOpen) return;
        if (event.key === 'Escape') {
            event.preventDefault();
            closeDriveSwitcher();
            return;
        }
        if (event.key !== 'Tab') return;
        const items = focusables();
        if (items.length === 0) return;
        const first = items[0];
        const last = items[items.length - 1];
        if (!sheetEl?.contains(document.activeElement)) {
            event.preventDefault();
            first.focus();
        } else if (event.shiftKey && document.activeElement === first) {
            event.preventDefault();
            last.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
            event.preventDefault();
            first.focus();
        }
    }

    // Drag to dismiss, on the same physics every other sheet in the app uses.
    // The grip is the universal "pull me down" mark, and one that does not move
    // reads as a sheet that is stuck; the grab area covers the title row with
    // it, because a 5px bar is not something a thumb aims at.
    let dragging = $state(false);
    let dragStartY = 0;
    let dragDelta = 0;
    const drag = createSheetDrag();

    function onGrabPointerDown(event: PointerEvent): void {
        const origin = event.target as HTMLElement;
        // Bound on the sheet rather than the grab area so the handlers sit on an
        // element that already carries a role; the origin is what decides.
        if (!sheetEl || !origin.closest('.switcher-grab') || origin.closest('.switcher-close')) return;
        dragging = true;
        dragStartY = event.clientY;
        dragDelta = 0;
        drag.start(event);
        sheetEl.style.transition = 'none';
        (event.currentTarget as HTMLElement).setPointerCapture?.(event.pointerId);
    }

    function onGrabPointerMove(event: PointerEvent): void {
        if (!dragging || !sheetEl) return;
        dragDelta = sheetOffset(event.clientY - dragStartY, sheetEl.offsetHeight);
        drag.track(event);
        sheetEl.style.transform = `translateY(${dragDelta}px)`;
    }

    function onGrabPointerUp(): void {
        if (!dragging || !sheetEl) return;
        dragging = false;
        const threshold = Math.max(88, sheetEl.offsetHeight * 0.28);
        const dismissed = shouldDismiss(dragDelta, drag.velocity(), threshold);
        dragDelta = 0;
        drag.reset();
        // Handing the sheet back to its own transition is what lets the release
        // carry on from where the finger left it, in either direction.
        sheetEl.style.transition = '';
        sheetEl.style.transform = '';
        if (dismissed) closeDriveSwitcher();
    }

    // Picking a drive row switches drives (handled by DriveList) and then
    // dismisses the sheet. The shared-drive actions button is a sibling, so it
    // never matches here and keeps the sheet open for its context menu.
    function onDriveNavClick(event: MouseEvent): void {
        if ((event.target as HTMLElement).closest('.drive-item')) closeDriveSwitcher();
    }
</script>

<svelte:window onkeydown={onKeydown} />

<!-- Decorative scrim; keyboard users dismiss via Escape, the close button, or
     hardware back, so it carries no key handler and stays out of the a11y tree. -->
<div
    bind:this={scrimEl}
    class="sheet-scrim"
    class:open={$driveSwitcherOpen}
    aria-hidden="true"
    onclick={closeDriveSwitcher}
></div>

<!-- The sheet stays in the DOM at all times: showing it is a transform, so it
     has to be there to move, and it is inert while closed. The drive lists are
     plain children of it rather than anything with a lifecycle of its own --
     they only read the sidebar store, so there is nothing to hold off until the
     dashboard is up, and they survive every open and close for free. -->
<div
    bind:this={sheetEl}
    class="mobile-sheet drive-switcher-sheet"
    class:open={$driveSwitcherOpen}
    role="dialog"
    aria-modal="true"
    aria-label="Drives"
    tabindex="-1"
    inert={!$driveSwitcherOpen}
    onpointerdown={onGrabPointerDown}
    onpointermove={onGrabPointerMove}
    onpointerup={onGrabPointerUp}
    onpointercancel={onGrabPointerUp}
>
    <!-- The grip and the title row are one grab area: the mark says the sheet
         moves, and the row beside it is what a thumb actually lands on. -->
    <div class="switcher-grab">
        <div class="switcher-grip" aria-hidden="true"><span></span></div>
        <div class="switcher-header">
            <h2 class="switcher-title">Drives</h2>
            <button type="button" class="switcher-close" aria-label="Close" onclick={closeDriveSwitcher}>
                <XIcon size={20} strokeWidth={2} aria-hidden="true" />
            </button>
        </div>
    </div>

    <!-- Delegated tap on the drive rows closes the sheet after the switch; the
         rows are real buttons, so keyboard activation flows through them and
         this listener needs no key handler of its own. -->
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions, a11y_click_events_have_key_events -->
    <nav id="drives-nav" class="switcher-list" tabindex="-1" aria-label="Drives" onclick={onDriveNavClick}>
        <!-- Two groups rather than one list: the phone shows the same split the
             desktop sidebar does, and each is its own stacking column. -->
        <div class="switcher-group">
            <DriveList kind="personal" onDriveClick={handleDriveClick} />
        </div>
        <div class="switcher-group">
            <DriveList
                kind="shared"
                onDriveClick={handleDriveClick}
                onDriveActions={showSharedActionsMenu}
                onPendingClick={handlePendingClick}
                onPendingActions={showPendingActionsMenu}
            />
        </div>
    </nav>

    <div class="switcher-actions">
        <button id="open-join-drive" class="switcher-action" type="button">
            <Link2Icon size={20} strokeWidth={1.9} aria-hidden="true" />
            Join with a link
        </button>
        <button id="open-new-drive" class="switcher-action" type="button">
            <FolderPlusIcon size={20} strokeWidth={1.9} aria-hidden="true" />
            Create a shared drive
        </button>
    </div>
</div>
