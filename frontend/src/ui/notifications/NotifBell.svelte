<script lang="ts">
    import { onDestroy, tick } from 'svelte';
    import BellIcon from '@lucide/svelte/icons/bell';
    import EventRow from './EventRow.svelte';
    import TransferRow from './TransferRow.svelte';
    import { hasActiveModal } from '../modals/modal-a11y';
    import { portal } from './portal';
    import {
        activeTransfers,
        bellMode,
        notifPanelOpen,
        notifUnreadErrors,
        recentEvents,
        type TransferDirection,
    } from './notif-store';

    interface Props {
        onCancelDirection: (direction: TransferDirection) => void;
        /** Stops one file listed under an aggregate row. */
        onCancelFile: (key: string) => void;
        onClearHistory: () => void;
    }

    let { onCancelDirection, onCancelFile, onClearHistory }: Props = $props();

    /**
     * Hover is an intent, not a click, in both directions. Closing has always
     * waited so a pointer can cross the gap between the bell and the panel;
     * opening waits for the same reason in reverse -- a pointer on its way to
     * the window controls passes over the bell, and without the delay that
     * throws a 380px panel over whatever the reader was looking at.
     */
    const HOVER_OPEN_MS = 140;
    const HOVER_CLOSE_MS = 140;

    /**
     * What Tab cycles through inside the panel. Narrower than the dialog list in
     * ui/modals/modal-a11y.ts on purpose: the panel holds buttons and the
     * container itself, and a selector that matched more would still have to
     * exclude everything the rows do not render.
     */
    const PANEL_FOCUSABLE = 'button:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])';

    let bellEl = $state<HTMLButtonElement | null>(null);
    let panelEl = $state<HTMLElement | null>(null);
    let anchor = $state({ top: 0, right: 0 });
    // One timer for both directions: the pointer is only ever arriving or
    // leaving, and a single slot makes the second intent cancel the first.
    let hoverTimer: ReturnType<typeof setTimeout> | null = null;

    /**
     * What the bell is about, not just that it exists. Colour alone carried
     * this before, which says nothing to a screen reader and little to anyone
     * who cannot separate the accent from the danger hue.
     */
    const bellLabel = $derived(
        $bellMode === 'error'
            ? `Notifications, ${$notifUnreadErrors} ${$notifUnreadErrors === 1 ? 'error' : 'errors'}`
            : $bellMode === 'active'
              ? `Notifications, ${$activeTransfers.length} transfer${$activeTransfers.length === 1 ? '' : 's'} in progress`
              : 'Notifications',
    );

    function reanchor(): void {
        if (!bellEl) return;
        const rect = bellEl.getBoundingClientRect();
        anchor = {
            top: rect.bottom + 10,
            right: Math.max(12, window.innerWidth - rect.right),
        };
    }

    function clearHoverTimer(): void {
        if (!hoverTimer) return;
        clearTimeout(hoverTimer);
        hoverTimer = null;
    }

    /**
     * Opens the panel. `focusPanel` is for deliberate activation only -- a
     * click or a key -- because hover must never pull focus away from whatever
     * the reader is actually typing into.
     */
    async function openPanel({ focusPanel = false }: { focusPanel?: boolean } = {}): Promise<void> {
        clearHoverTimer();
        reanchor();
        notifPanelOpen.set(true);
        notifUnreadErrors.set(0);
        if (!focusPanel) return;
        await tick();
        // The container, not its first button: a pointer-opened panel shows no
        // focus ring this way, while Tab still steps into the controls.
        panelEl?.focus({ preventScroll: true });
    }

    function closePanel({ restoreFocus = false }: { restoreFocus?: boolean } = {}): void {
        clearHoverTimer();
        // Closing unmounts whatever held focus, which would strand it on
        // <body>. The bell takes it back whenever it was inside, not only when
        // the caller asked -- otherwise Tab restarts from the top of the page.
        const heldFocus = panelEl?.contains(document.activeElement) ?? false;
        notifPanelOpen.set(false);
        if (restoreFocus || heldFocus) bellEl?.focus({ preventScroll: true });
    }

    /** Activation toggles; aria-expanded would otherwise be a standing lie. */
    function togglePanel(): void {
        if ($notifPanelOpen) closePanel({ restoreFocus: true });
        else void openPanel({ focusPanel: true });
    }

    function scheduleOpenPanel(): void {
        clearHoverTimer();
        if ($notifPanelOpen) return;
        hoverTimer = setTimeout(() => {
            hoverTimer = null;
            void openPanel();
        }, HOVER_OPEN_MS);
    }

    function scheduleClosePanel(): void {
        // Also drops a pending open, so a pointer that only passes over the
        // bell leaves nothing behind.
        clearHoverTimer();
        hoverTimer = setTimeout(() => {
            hoverTimer = null;
            closePanel();
        }, HOVER_CLOSE_MS);
    }

    function onBellKeydown(event: KeyboardEvent): void {
        if (event.key !== 'Enter' && event.key !== ' ') return;
        // Swallowed here so the synthesised click cannot toggle a second time.
        event.preventDefault();
        togglePanel();
    }

    function panelFocusables(): HTMLElement[] {
        if (!panelEl) return [];
        return Array.from(panelEl.querySelectorAll<HTMLElement>(PANEL_FOCUSABLE));
    }

    /**
     * Keeps Tab inside the panel. It hangs off the panel rather than the
     * document so containment applies exactly while focus is in there: a
     * hover-opened panel nobody has focused leaves Tab alone, which is what
     * keeps this a popover rather than a modal.
     */
    function onPanelKeydown(event: KeyboardEvent): void {
        if (event.key !== 'Tab') return;
        const items = panelFocusables();
        if (items.length === 0) {
            event.preventDefault();
            return;
        }
        const first = items[0];
        const last = items[items.length - 1];
        const active = document.activeElement;
        // Only the edges are steered; the browser still walks the middle.
        if (active === panelEl) {
            // Where opening left focus: the first Tab steps into the controls.
            event.preventDefault();
            (event.shiftKey ? last : first).focus({ preventScroll: true });
        } else if (event.shiftKey && active === first) {
            event.preventDefault();
            last.focus({ preventScroll: true });
        } else if (!event.shiftKey && active === last) {
            event.preventDefault();
            first.focus({ preventScroll: true });
        }
    }

    function onWindowKeydown(event: KeyboardEvent): void {
        if (event.key !== 'Escape' || hasActiveModal() || !$notifPanelOpen) return;
        event.preventDefault();
        event.stopPropagation();
        closePanel({ restoreFocus: true });
    }

    function onDocumentMousedown(event: MouseEvent): void {
        if (!$notifPanelOpen) return;
        const target = event.target as Node;
        if (bellEl?.contains(target)) return;
        if ((target as HTMLElement).closest?.('.notif-panel')) return;
        closePanel();
    }

    onDestroy(clearHoverTimer);
</script>

<svelte:window onkeydown={onWindowKeydown} onresize={reanchor} />
<svelte:document onmousedown={onDocumentMousedown} />

<button
    bind:this={bellEl}
    id="notif-bell"
    class="notif-bell"
    type="button"
    data-mode={$bellMode}
    aria-haspopup="dialog"
    aria-controls="notif-panel"
    aria-expanded={$notifPanelOpen ? 'true' : 'false'}
    aria-label={bellLabel}
    onclick={(event) => {
        event.stopPropagation();
        togglePanel();
    }}
    onkeydown={onBellKeydown}
    onmouseenter={scheduleOpenPanel}
    onmouseleave={scheduleClosePanel}
>
    <span class="notif-bell-icon" aria-hidden="true">
        <BellIcon size={18} strokeWidth={1.8} aria-hidden="true" />
    </span>
    <span class="notif-bell-dot" data-mode={$bellMode} aria-hidden="true"></span>
</button>


{#if $notifPanelOpen}
    <div
        bind:this={panelEl}
        id="notif-panel"
        class="notif-panel"
        role="dialog"
        aria-modal="false"
        aria-labelledby="notif-panel-title"
        tabindex="-1"
        style={`top:${anchor.top}px; right:${anchor.right}px;`}
        use:portal
        onmouseenter={clearHoverTimer}
        onmouseleave={scheduleClosePanel}
        onkeydown={onPanelKeydown}
        onclick={(event) => event.stopPropagation()}
    >
        <div class="notif-panel-header">
            <h2 id="notif-panel-title" class="notif-panel-title">Notifications</h2>
            <div class="notif-panel-actions">
                <button
                    class="notif-panel-clear"
                    type="button"
                    disabled={$recentEvents.length === 0}
                    onclick={onClearHistory}
                >
                    Clear
                </button>
            </div>
        </div>
        <div class="notif-panel-body">
            {#if $activeTransfers.length > 0}
                <h3 id="notif-section-active" class="notif-section-label">Active</h3>
                <div class="notif-section" role="list" aria-labelledby="notif-section-active">
                    {#each $activeTransfers as transfer (transfer.id)}
                        <!-- TransferRow carries no role of its own, unlike its
                             phone twin; the wrapper supplies one until it grows
                             EventRow's `listItem` prop. -->
                        <div role="listitem">
                            <TransferRow {transfer} onCancel={onCancelDirection} {onCancelFile} />
                        </div>
                    {/each}
                </div>
            {/if}
            {#if $recentEvents.length > 0}
                <h3 id="notif-section-recent" class="notif-section-label">Recent</h3>
                <div class="notif-section" role="list" aria-labelledby="notif-section-recent">
                    {#each $recentEvents.slice(0, 50) as entry (entry.id)}
                        {#if entry.kind === 'transfer'}
                            <div role="listitem"><TransferRow transfer={entry} /></div>
                        {:else}
                            <EventRow event={entry} listItem />
                        {/if}
                    {/each}
                </div>
            {:else if $activeTransfers.length === 0}
                <div class="notif-empty">
                    <div class="notif-empty-glyph">
                        <BellIcon size={48} strokeWidth={1.8} aria-hidden="true" />
                    </div>
                    <div class="notif-empty-title">All caught up</div>
                    <div class="notif-empty-body">Folder activity, uploads, and shared-drive events will show up here.</div>
                </div>
            {/if}
        </div>
    </div>
{/if}
