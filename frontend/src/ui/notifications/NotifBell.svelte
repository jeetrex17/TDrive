<script lang="ts">
    import { onDestroy } from 'svelte';
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

    const HOVER_CLOSE_MS = 140;

    let bellEl = $state<HTMLButtonElement | null>(null);
    let anchor = $state({ top: 0, right: 0 });
    let closeTimer: ReturnType<typeof setTimeout> | null = null;

    function reanchor(): void {
        if (!bellEl) return;
        const rect = bellEl.getBoundingClientRect();
        anchor = {
            top: rect.bottom + 10,
            right: Math.max(12, window.innerWidth - rect.right),
        };
    }

    function clearCloseTimer(): void {
        if (!closeTimer) return;
        clearTimeout(closeTimer);
        closeTimer = null;
    }

    function openPanel(): void {
        clearCloseTimer();
        reanchor();
        notifPanelOpen.set(true);
        notifUnreadErrors.set(0);
    }

    function closePanel({ restoreFocus = false }: { restoreFocus?: boolean } = {}): void {
        clearCloseTimer();
        notifPanelOpen.set(false);
        if (restoreFocus) bellEl?.focus({ preventScroll: true });
    }

    function scheduleClosePanel(): void {
        clearCloseTimer();
        closeTimer = setTimeout(() => closePanel(), HOVER_CLOSE_MS);
    }

    function onBellEnter(): void {
        openPanel();
    }

    function onBellKeydown(event: KeyboardEvent): void {
        if (event.key === 'Enter' || event.key === ' ') {
            event.preventDefault();
            openPanel();
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

    onDestroy(clearCloseTimer);
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
    aria-label="Notifications"
    onclick={(event) => {
        event.stopPropagation();
        openPanel();
    }}
    onkeydown={onBellKeydown}
    onmouseenter={onBellEnter}
    onmouseleave={scheduleClosePanel}
>
    <span class="notif-bell-icon" aria-hidden="true">
        <BellIcon size={18} strokeWidth={1.8} aria-hidden="true" />
    </span>
    <span class="notif-bell-dot" data-mode={$bellMode} aria-hidden="true"></span>
</button>


{#if $notifPanelOpen}
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <div
        id="notif-panel"
        class="notif-panel"
        role="dialog"
        aria-modal="false"
        aria-labelledby="notif-panel-title"
        tabindex="-1"
        style={`top:${anchor.top}px; right:${anchor.right}px;`}
        use:portal
        onmouseenter={clearCloseTimer}
        onmouseleave={scheduleClosePanel}
        onclick={(event) => event.stopPropagation()}
    >
        <div class="notif-panel-header">
            <div id="notif-panel-title" class="notif-panel-title">Notifications</div>
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
                <div class="notif-section-label">Active</div>
                <div class="notif-section">
                    {#each $activeTransfers as transfer (transfer.id)}
                        <TransferRow {transfer} onCancel={onCancelDirection} {onCancelFile} />
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
