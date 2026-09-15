<script lang="ts">
    import { tick } from 'svelte';
    import FileIcon from '@lucide/svelte/icons/file';
    import FolderIcon from '@lucide/svelte/icons/folder';
    import { IOS, Android } from '@wailsio/runtime';
    import { contextMenuState, hideContextMenu, type ContextMenuItem } from './context-menu-store';
    import { isAndroidPlatform, isGatewayReady, isIOSPlatform, isMobilePlatform } from '../../api';

    const VIEWPORT_MARGIN = 8;

    // The store feeds both surfaces; the phone renders a bottom action sheet,
    // every other platform the anchored popover. Resolved once: platform is
    // fixed for the session.
    const asSheet = isMobilePlatform();

    let panel = $state<HTMLElement | null>(null);
    let sheet = $state<HTMLElement | null>(null);
    let left = $state(0);
    let top = $state(0);
    let lastFocusVersion = 0;
    let invoker: HTMLElement | null = null;

    let dragging = false;
    let dragStartY = 0;
    let dragDelta = 0;

    function container(): HTMLElement | null {
        return asSheet ? sheet : panel;
    }

    function menuButtons(): HTMLButtonElement[] {
        const root = container();
        if (!root) return [];
        return Array.from(root.querySelectorAll<HTMLButtonElement>('button[role="menuitem"]:not(:disabled)'));
    }

    function focusMenuItem(delta: number): void {
        const items = menuButtons();
        if (!items.length) return;
        const current = document.activeElement instanceof HTMLButtonElement
            ? items.indexOf(document.activeElement)
            : -1;
        const next = current < 0 ? 0 : (current + delta + items.length) % items.length;
        items[next]?.focus();
    }

    function focusMenuEdge(edge: 'first' | 'last'): void {
        const items = menuButtons();
        const target = edge === 'first' ? items[0] : items[items.length - 1];
        target?.focus();
    }

    function captureInvoker(): void {
        const active = document.activeElement;
        if (active instanceof HTMLElement && !container()?.contains(active)) invoker = active;
    }

    function focusInvoker(): void {
        const target = invoker;
        invoker = null;
        if (!target?.isConnected || target.hasAttribute('disabled')) return;
        target.focus({ preventScroll: true });
    }

    async function dismissAndRestoreFocus(): Promise<void> {
        hideContextMenu();
        await tick();
        focusInvoker();
    }

    // Light impact when the action sheet appears. Guarded so the browser
    // preview (no native bridge) never calls into the runtime and never throws.
    function openHaptic(): void {
        if (!asSheet || !isGatewayReady()) return;
        try {
            if (isIOSPlatform()) void IOS.Haptics.Impact('light').catch(() => {});
            else if (isAndroidPlatform()) void Android.Haptics.Vibrate(20).catch(() => {});
        } catch {
            // A haptic is a courtesy; a missing generator must not break the menu.
        }
    }

    async function positionHost(): Promise<void> {
        const state = $contextMenuState;
        if (!state.open) return;

        if (asSheet) {
            if (lastFocusVersion !== state.focusVersion) {
                captureInvoker();
                lastFocusVersion = state.focusVersion;
                openHaptic();
                await tick();
                // Focus the sheet, not the first row: a touch-open shows no focus
                // ring, while arrow keys still move into the rows for keyboard use.
                sheet?.focus({ preventScroll: true });
            }
            return;
        }

        left = state.x;
        top = state.y;
        await tick();

        if (!panel) return;
        const rect = panel.getBoundingClientRect();
        const maxX = Math.max(VIEWPORT_MARGIN, window.innerWidth - rect.width - VIEWPORT_MARGIN);
        const maxY = Math.max(VIEWPORT_MARGIN, window.innerHeight - rect.height - VIEWPORT_MARGIN);
        left = Math.max(VIEWPORT_MARGIN, Math.min(state.x, maxX));
        top = Math.max(VIEWPORT_MARGIN, Math.min(state.y, maxY));

        if (lastFocusVersion !== state.focusVersion) {
            captureInvoker();
            lastFocusVersion = state.focusVersion;
            await tick();
            focusMenuEdge('first');
        }
    }

    function invoke(item: ContextMenuItem): void {
        if (item.type === 'divider' || item.disabled) return;
        hideContextMenu();
        // Hand the invoker to actions synchronously. A dialog opened by the
        // action records it as its restore target; navigation remains free to
        // establish its own focus without a delayed menu restoration.
        focusInvoker();
        void item.action();
    }

    function onDocumentClick(event: MouseEvent): void {
        if (!$contextMenuState.open) return;
        if (container()?.contains(event.target as Node)) return;
        invoker = null;
        hideContextMenu();
    }

    function onDocumentKeydown(event: KeyboardEvent): void {
        if (!$contextMenuState.open) return;
        if (event.key === 'Tab') {
            hideContextMenu();
            focusInvoker();
            return;
        }
        if (event.key === 'Escape') {
            event.preventDefault();
            void dismissAndRestoreFocus();
            return;
        }
        if (event.key === 'ArrowDown') {
            event.preventDefault();
            focusMenuItem(1);
            return;
        }
        if (event.key === 'ArrowUp') {
            event.preventDefault();
            focusMenuItem(-1);
            return;
        }
        if (event.key === 'Home') {
            event.preventDefault();
            focusMenuEdge('first');
            return;
        }
        if (event.key === 'End') {
            event.preventDefault();
            focusMenuEdge('last');
        }
    }

    // --- action-sheet swipe to dismiss ---------------------------------------

    function onHandlePointerDown(event: PointerEvent): void {
        if (!sheet) return;
        dragging = true;
        dragStartY = event.clientY;
        dragDelta = 0;
        sheet.style.transition = '';
        (event.currentTarget as HTMLElement).setPointerCapture?.(event.pointerId);
    }

    function onHandlePointerMove(event: PointerEvent): void {
        if (!dragging || !sheet) return;
        dragDelta = Math.max(0, event.clientY - dragStartY);
        sheet.style.transform = `translateY(${dragDelta}px)`;
    }

    function onHandlePointerUp(): void {
        if (!dragging || !sheet) return;
        dragging = false;
        const threshold = Math.max(72, sheet.offsetHeight * 0.3);
        if (dragDelta > threshold) {
            void dismissAndRestoreFocus();
        } else {
            sheet.style.transition = 'transform var(--motion-med) var(--ease-standard)';
            sheet.style.transform = 'translateY(0)';
        }
        dragDelta = 0;
    }

    $effect(() => {
        void positionHost();
    });
</script>

<svelte:document onclick={onDocumentClick} onkeydown={onDocumentKeydown} />

{#if $contextMenuState.open}
    {#if asSheet}
        <div class="action-sheet-scrim" aria-hidden="true"></div>
        <div class="action-sheet" role="menu" tabindex="-1" bind:this={sheet}>
            <div
                class="sheet-handle"
                aria-hidden="true"
                onpointerdown={onHandlePointerDown}
                onpointermove={onHandlePointerMove}
                onpointerup={onHandlePointerUp}
                onpointercancel={onHandlePointerUp}
            >
                <span></span>
            </div>

            {#if $contextMenuState.header}
                <div class="action-sheet-header">
                    <span class="action-sheet-icon" aria-hidden="true">
                        {#if $contextMenuState.header.kind === 'folder'}
                            <FolderIcon size={22} strokeWidth={1.75} />
                        {:else}
                            <FileIcon size={22} strokeWidth={1.75} />
                        {/if}
                    </span>
                    <span class="action-sheet-heading">
                        <span class="action-sheet-title">{$contextMenuState.header.title}</span>
                        {#if $contextMenuState.header.meta}
                            <span class="action-sheet-meta">{$contextMenuState.header.meta}</span>
                        {/if}
                    </span>
                </div>
            {/if}

            <div class="action-sheet-items">
                {#each $contextMenuState.items as item, index (item.type === 'divider' ? `divider-${index}` : `${item.label}-${index}`)}
                    {#if item.type === 'divider'}
                        <div class="action-sheet-sep" role="separator"></div>
                    {:else}
                        <button
                            type="button"
                            role="menuitem"
                            class="action-sheet-row"
                            class:danger={item.danger}
                            disabled={item.disabled}
                            tabindex="-1"
                            onclick={() => invoke(item)}
                        >
                            {item.label}
                        </button>
                    {/if}
                {/each}
            </div>

            <button type="button" class="action-sheet-cancel" onclick={() => void dismissAndRestoreFocus()}>
                Cancel
            </button>
        </div>
    {:else}
        <div
            bind:this={panel}
            class="context-menu-panel"
            role="menu"
            style:left={`${left}px`}
            style:top={`${top}px`}
        >
            {#each $contextMenuState.items as item, index (item.type === 'divider' ? `divider-${index}` : `${item.label}-${index}`)}
                {#if item.type === 'divider'}
                    <div class="divider" role="separator"></div>
                {:else}
                    <button
                        type="button"
                        role="menuitem"
                        class:danger={item.danger}
                        disabled={item.disabled}
                        tabindex="-1"
                        onclick={() => invoke(item)}
                    >
                        {item.label}
                    </button>
                {/if}
            {/each}
        </div>
    {/if}
{/if}
