<script lang="ts">
    import FolderPlusIcon from '@lucide/svelte/icons/folder-plus';
    import Link2Icon from '@lucide/svelte/icons/link-2';
    import XIcon from '@lucide/svelte/icons/x';
    import { driveSwitcherOpen, closeDriveSwitcher } from './mobile-shell-store';

    let sheetEl = $state<HTMLElement | null>(null);

    // Focus into the sheet when it opens so keyboard and screen-reader users land
    // inside it, not behind it.
    $effect(() => {
        if ($driveSwitcherOpen && sheetEl) {
            const target = sheetEl.querySelector<HTMLElement>('.drive-item, #open-join-drive');
            (target ?? sheetEl).focus({ preventScroll: true });
        }
    });

    function onKeydown(event: KeyboardEvent): void {
        if (event.key === 'Escape' && $driveSwitcherOpen) {
            event.preventDefault();
            closeDriveSwitcher();
        }
    }

    // Picking a drive row switches drives (handled by the portaled DriveList) and
    // then dismisses the sheet. The shared-drive actions button is a sibling, so
    // it never matches here and keeps the sheet open for its context menu.
    function onDriveNavClick(event: MouseEvent): void {
        if ((event.target as HTMLElement).closest('.drive-item')) closeDriveSwitcher();
    }
</script>

<svelte:window onkeydown={onKeydown} />

<!-- Decorative scrim; keyboard users dismiss via Escape, the close button, or
     hardware back, so it carries no key handler and stays out of the a11y tree. -->
<div
    class="sheet-scrim"
    class:open={$driveSwitcherOpen}
    aria-hidden="true"
    onclick={closeDriveSwitcher}
></div>

<!-- The sheet stays in the DOM at all times so the drive lists that portal into
     #drives-personal / #drives-shared are never unmounted; visibility is a
     transform, and it is inert while closed. -->
<div
    bind:this={sheetEl}
    class="mobile-sheet drive-switcher-sheet"
    class:open={$driveSwitcherOpen}
    role="dialog"
    aria-modal="true"
    aria-label="Drives"
    tabindex="-1"
    inert={!$driveSwitcherOpen}
>
    <div class="switcher-grip" aria-hidden="true"><span></span></div>
    <div class="switcher-header">
        <h2 class="switcher-title">Drives</h2>
        <button type="button" class="switcher-close" aria-label="Close" onclick={closeDriveSwitcher}>
            <XIcon size={20} strokeWidth={2} aria-hidden="true" />
        </button>
    </div>

    <!-- Delegated tap on the portaled drive rows closes the sheet after the
         switch; the rows are real buttons, so keyboard activation flows through
         them and this listener needs no key handler of its own. -->
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions, a11y_click_events_have_key_events -->
    <nav id="drives-nav" class="switcher-list" tabindex="-1" aria-label="Drives" onclick={onDriveNavClick}>
        <div id="drives-personal" class="switcher-group"></div>
        <div id="drives-shared" class="switcher-group"></div>
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
