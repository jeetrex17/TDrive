<script lang="ts">
    import { tick } from 'svelte';
    import FileIcon from '@lucide/svelte/icons/file';
    import FolderIcon from '@lucide/svelte/icons/folder';
    import EyeIcon from '@lucide/svelte/icons/eye';
    import PlayIcon from '@lucide/svelte/icons/play';
    import DownloadIcon from '@lucide/svelte/icons/download';
    import PencilIcon from '@lucide/svelte/icons/pencil';
    import FolderInputIcon from '@lucide/svelte/icons/folder-input';
    import Trash2Icon from '@lucide/svelte/icons/trash-2';
    import UploadIcon from '@lucide/svelte/icons/upload';
    import FolderPlusIcon from '@lucide/svelte/icons/folder-plus';
    import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';
    import FileTextIcon from '@lucide/svelte/icons/file-text';
    import DatabaseIcon from '@lucide/svelte/icons/database';
    import CalendarIcon from '@lucide/svelte/icons/calendar';
    import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
    import { IOS, Android } from '@wailsio/runtime';
    import {
        contextMenuState,
        hideContextMenu,
        type ContextMenuDetailIcon,
        type ContextMenuIcon,
        type ContextMenuItem,
    } from './context-menu-store';
    import { fileTypeFamily, fileTypeIcon } from '../file-list/file-type';
    import { isAndroidPlatform, isGatewayReady, isIOSPlatform, isMobilePlatform } from '../../api';

    // The store names an icon; this module owns what that name looks like, so
    // the action builders never import a component.
    const ICONS: Record<ContextMenuIcon, typeof FileIcon> = {
        open: EyeIcon,
        play: PlayIcon,
        download: DownloadIcon,
        rename: PencilIcon,
        move: FolderInputIcon,
        delete: Trash2Icon,
        upload: UploadIcon,
        'folder-new': FolderPlusIcon,
        refresh: RefreshCwIcon,
    };

    const DETAIL_ICONS: Record<ContextMenuDetailIcon, typeof FileIcon> = {
        type: FileTextIcon,
        size: DatabaseIcon,
        added: CalendarIcon,
        location: FolderIcon,
    };

    /** The header glyph: the same type icon the row showed, not a generic page. */
    const headerIcon = $derived.by(() => {
        const header = $contextMenuState.header;
        if (!header) return FileIcon;
        if (header.kind === 'folder') return FolderIcon;
        return fileTypeIcon(fileTypeFamily(header.ext ?? ''));
    });

    // Only a sheet with a header promotes actions to tiles. A menu opened on
    // empty space has no subject, so it stays a plain list.
    const tiles = $derived(
        $contextMenuState.header
            ? $contextMenuState.items.filter((item) => item.type !== 'divider' && item.primary)
            : [],
    );
    const listItems = $derived.by(() => {
        if (!tiles.length) return $contextMenuState.items;
        const rest = $contextMenuState.items.filter((item) => item.type === 'divider' || !item.primary);
        // Pulling items out can strand a separator at either end, where it
        // would draw a rule against the sheet's own edge.
        while (rest.length && rest[0].type === 'divider') rest.shift();
        while (rest.length && rest[rest.length - 1].type === 'divider') rest.pop();
        return rest;
    });

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
    // A short history rather than the last event alone: one sample is noisy and
    // a finger that pauses before lifting would read as a flick.
    let dragSamples: Array<{ y: number; t: number }> = [];

    /** Release speed in px/s, measured across the recent samples. */
    function releaseVelocity(): number {
        if (dragSamples.length < 2) return 0;
        const first = dragSamples[0];
        const last = dragSamples[dragSamples.length - 1];
        const elapsed = last.t - first.t;
        if (elapsed <= 0) return 0;
        return ((last.y - first.y) / elapsed) * 1000;
    }

    /**
     * Where the sheet would come to rest if released now, using the same
     * exponential decay a scroll view uses. Deciding on the projected point
     * rather than the point under the finger is what lets a short, fast flick
     * dismiss while a long, slow drag that stopped does not.
     */
    function projectedOffset(velocity: number): number {
        const DECELERATION = 0.998;
        return (velocity / 1000) * DECELERATION / (1 - DECELERATION);
    }

    /**
     * Dragging the sheet up has nowhere to go, so it resists progressively
     * instead of stopping dead. A hard stop reads as frozen; resistance reads
     * as "responsive, but there is nothing more here".
     */
    function rubberband(overshoot: number, dimension: number): number {
        const CONSTANT = 0.55;
        return (overshoot * dimension * CONSTANT) / (dimension + CONSTANT * Math.abs(overshoot));
    }

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
        dragSamples = [{ y: event.clientY, t: event.timeStamp }];
        sheet.style.transition = '';
        (event.currentTarget as HTMLElement).setPointerCapture?.(event.pointerId);
    }

    function onHandlePointerMove(event: PointerEvent): void {
        if (!dragging || !sheet) return;
        const raw = event.clientY - dragStartY;
        dragDelta = raw >= 0 ? raw : rubberband(raw, sheet.offsetHeight);
        dragSamples.push({ y: event.clientY, t: event.timeStamp });
        if (dragSamples.length > 5) dragSamples.shift();
        sheet.style.transform = `translateY(${dragDelta}px)`;
    }

    function onHandlePointerUp(): void {
        if (!dragging || !sheet) return;
        dragging = false;
        const velocity = releaseVelocity();
        const threshold = Math.max(72, sheet.offsetHeight * 0.3);
        // Judge where the gesture was going, not where the finger happened to
        // stop. A flick throws the sheet; a drag that halted keeps it.
        if (dragDelta + projectedOffset(velocity) > threshold) {
            void dismissAndRestoreFocus();
        } else {
            // Settle faster when the finger was still moving, so the return
            // continues the gesture instead of restarting at zero speed.
            const settle = velocity > 600 ? 'var(--motion-fast)' : 'var(--motion-med)';
            sheet.style.transition = `transform ${settle} var(--ease-enter)`;
            sheet.style.transform = 'translateY(0)';
        }
        dragDelta = 0;
        dragSamples = [];
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
                {@const HeaderIcon = headerIcon}
                <div class="action-sheet-header">
                    <span
                        class="action-sheet-icon"
                        class:is-folder={$contextMenuState.header.kind === 'folder'}
                        aria-hidden="true"
                    >
                        <HeaderIcon size={40} strokeWidth={1.5} />
                    </span>
                    <span class="action-sheet-heading">
                        <span class="action-sheet-title">{$contextMenuState.header.title}</span>
                        {#if $contextMenuState.header.meta}
                            <span class="action-sheet-meta">{$contextMenuState.header.meta}</span>
                        {/if}
                    </span>
                </div>
            {/if}

            {#if tiles.length}
                <div class="action-sheet-tiles">
                    {#each tiles as item, index (`${item.type === 'divider' ? 'd' : item.label}-${index}`)}
                        {#if item.type !== 'divider'}
                            <button
                                type="button"
                                role="menuitem"
                                class="action-sheet-tile"
                                disabled={item.disabled}
                                tabindex="-1"
                                onclick={() => invoke(item)}
                            >
                                {#if item.icon}
                                    {@const TileIcon = ICONS[item.icon]}
                                    <TileIcon size={22} strokeWidth={1.9} aria-hidden="true" />
                                {/if}
                                <span class="action-sheet-tile-label">{item.label.replace(/…$/, '')}</span>
                            </button>
                        {/if}
                    {/each}
                </div>
            {/if}

            {#if $contextMenuState.header?.details?.length}
                <dl class="action-sheet-details">
                    {#each $contextMenuState.header.details as detail (detail.label)}
                        <div class="action-sheet-detail">
                            <dt>
                                {#if detail.icon}
                                    {@const DetailIcon = DETAIL_ICONS[detail.icon]}
                                    <DetailIcon size={16} strokeWidth={1.9} aria-hidden="true" />
                                {/if}
                                <span>{detail.label}</span>
                            </dt>
                            <dd>
                                <span>{detail.value}</span>
                                {#if detail.icon === 'location'}
                                    <ChevronRightIcon size={15} strokeWidth={2} aria-hidden="true" />
                                {/if}
                            </dd>
                        </div>
                    {/each}
                </dl>
            {/if}

            <div class="action-sheet-items">
                {#each listItems as item, index (item.type === 'divider' ? `divider-${index}` : `${item.label}-${index}`)}
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
                            {#if item.icon}
                                {@const RowIcon = ICONS[item.icon]}
                                <RowIcon size={20} strokeWidth={1.9} aria-hidden="true" />
                            {/if}
                            <span>{item.label}</span>
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
