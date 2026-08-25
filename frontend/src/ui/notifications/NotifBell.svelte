<script lang="ts">
    import BellIcon from '@lucide/svelte/icons/bell';
    import EventRow from './EventRow.svelte';
    import TransferRow from './TransferRow.svelte';
    import { portal } from './portal';
    import {
        activeTransfers,
        bellMode,
        notifHoverOpen,
        notifPanelOpen,
        notifUnreadErrors,
        recentEvents,
        type TransferDirection,
    } from './notif-store';

    interface Props {
        onCancelDirection: (direction: TransferDirection) => void;
        onClearHistory: () => void;
    }

    let { onCancelDirection, onClearHistory }: Props = $props();

    const HOVER_GRACE_MS = 280;

    let bellEl = $state<HTMLButtonElement | null>(null);
    let anchor = $state({ top: 0, right: 0 });
    let hoverGraceTimer: ReturnType<typeof setTimeout> | null = null;

    function surfaceOut(_node: Element) {
        // Mirrors the legacy .notif-leaving fade.
        return { duration: 160, css: (t: number) => `opacity: ${t};` };
    }

    function reanchor(): void {
        if (!bellEl) return;
        const rect = bellEl.getBoundingClientRect();
        anchor = {
            top: rect.bottom + 10,
            right: Math.max(12, window.innerWidth - rect.right),
        };
    }

    function clearGraceTimer(): void {
        if (hoverGraceTimer) {
            clearTimeout(hoverGraceTimer);
            hoverGraceTimer = null;
        }
    }

    function openPanel(): void {
        closeHover();
        reanchor();
        notifPanelOpen.set(true);
        notifUnreadErrors.set(0); // opening clears the unread badge
    }

    function closePanel(): void {
        notifPanelOpen.set(false);
    }

    function togglePanel(): void {
        if ($notifPanelOpen) closePanel();
        else openPanel();
    }

    function openHover(): void {
        if ($notifPanelOpen || $activeTransfers.length === 0) return;
        reanchor();
        notifHoverOpen.set(true);
    }

    function closeHover(): void {
        clearGraceTimer();
        notifHoverOpen.set(false);
    }

    function onBellEnter(): void {
        if ($notifPanelOpen) return;
        clearGraceTimer();
        openHover();
    }

    function onBellLeave(): void {
        clearGraceTimer();
        hoverGraceTimer = setTimeout(() => closeHover(), HOVER_GRACE_MS);
    }

    function onBellKeydown(event: KeyboardEvent): void {
        if (event.key === 'Enter' || event.key === ' ') {
            event.preventDefault();
            togglePanel();
        }
    }

    function onWindowKeydown(event: KeyboardEvent): void {
        if (event.key !== 'Escape') return;
        if ($notifPanelOpen) closePanel();
        else if ($notifHoverOpen) closeHover();
    }

    function onDocumentMousedown(event: MouseEvent): void {
        if (!$notifPanelOpen) return;
        const target = event.target as Node;
        if (bellEl?.contains(target)) return;
        if ((target as HTMLElement).closest?.('.notif-panel')) return;
        closePanel();
    }

    // The hover popover only exists while transfers run; drop it the moment
    // the last active transfer settles.
    $effect(() => {
        if ($notifHoverOpen && $activeTransfers.length === 0) closeHover();
    });
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
    aria-expanded={$notifPanelOpen ? 'true' : 'false'}
    aria-label="Notifications"
    onclick={(event) => {
        event.stopPropagation();
        togglePanel();
    }}
    onkeydown={onBellKeydown}
    onmouseenter={onBellEnter}
    onmouseleave={onBellLeave}
>
    <span class="notif-bell-icon" aria-hidden="true">
        <BellIcon size={18} strokeWidth={1.8} aria-hidden="true" />
    </span>
    <span class="notif-bell-dot" data-mode={$bellMode} aria-hidden="true"></span>
</button>

{#if $notifHoverOpen}
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <div
        class="notif-popover"
        role="dialog"
        aria-label="Active transfers"
        tabindex="-1"
        style={`top:${anchor.top}px; right:${anchor.right}px;`}
        use:portal
        out:surfaceOut
        onmouseenter={clearGraceTimer}
        onmouseleave={onBellLeave}
        onclick={(event) => {
            // Click into the popover upgrades it to the full panel.
            event.stopPropagation();
            openPanel();
        }}
    >
        <div class="notif-popover-list">
            {#each $activeTransfers.slice(0, 4) as transfer (transfer.id)}
                <TransferRow {transfer} onCancel={onCancelDirection} />
            {/each}
        </div>
        {#if $activeTransfers.length > 4}
            <div class="notif-popover-more">+ {$activeTransfers.length - 4} more</div>
        {/if}
    </div>
{/if}

{#if $notifPanelOpen}
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <div
        class="notif-panel"
        role="dialog"
        aria-modal="false"
        aria-label="Notifications"
        tabindex="-1"
        style={`top:${anchor.top}px; right:${anchor.right}px;`}
        use:portal
        out:surfaceOut
        onclick={(event) => event.stopPropagation()}
    >
        <div class="notif-panel-header">
            <div class="notif-panel-title">Notifications</div>
            <button
                class="notif-panel-clear"
                type="button"
                disabled={$recentEvents.length === 0}
                onclick={onClearHistory}
            >
                Clear
            </button>
        </div>
        <div class="notif-panel-body">
            {#if $activeTransfers.length > 0}
                <div class="notif-section-label">Active</div>
                <div class="notif-section">
                    {#each $activeTransfers as transfer (transfer.id)}
                        <TransferRow {transfer} onCancel={onCancelDirection} />
                    {/each}
                </div>
            {/if}
            {#if $recentEvents.length > 0}
                <div class="notif-section-label" style={`margin-top:${$activeTransfers.length > 0 ? '14px' : '0'}`}>Recent</div>
                <div class="notif-section">
                    {#each $recentEvents.slice(0, 50) as entry (entry.id)}
                        {#if entry.kind === 'transfer'}
                            <TransferRow transfer={entry} />
                        {:else}
                            <EventRow event={entry} />
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
