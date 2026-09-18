<script lang="ts">
    import ClockIcon from '@lucide/svelte/icons/clock';
    import EllipsisIcon from '@lucide/svelte/icons/ellipsis';
    import FolderIcon from '@lucide/svelte/icons/folder';
    import type { DriveChannel, PendingJoin } from '../../types';
    import { contextMenuState } from '../menus/context-menu-store';
    import { sidebarState, type SidebarActionMenuRequest } from './sidebar-store';

    type DriveKind = 'personal' | 'shared';
    type ActionMenuHandler<T> = (request: SidebarActionMenuRequest, value: T) => void;

    interface Props {
        kind: DriveKind;
        onDriveClick: (channelId: number) => void;
        onDriveActions?: ActionMenuHandler<DriveChannel>;
        onPendingClick?: (inviteHash: string) => void;
        onPendingActions?: ActionMenuHandler<PendingJoin>;
    }

    let {
        kind,
        onDriveClick,
        onDriveActions,
        onPendingClick,
        onPendingActions,
    }: Props = $props();

    let activeActionKey = $state<string | null>(null);
    let returnFocusTo = $state<HTMLElement | null>(null);
    let menuOpenedByThisList = $state(false);

    $effect(() => {
        const menuOpen = $contextMenuState.open;
        if (!activeActionKey) return;
        if (menuOpen) {
            menuOpenedByThisList = true;
            return;
        }
        if (!menuOpenedByThisList) return;

        const target = returnFocusTo;
        activeActionKey = null;
        returnFocusTo = null;
        menuOpenedByThisList = false;
        queueMicrotask(() => {
            if (target?.isConnected) target.focus();
        });
    });

    function titleFor(channel: DriveChannel): string {
        return channel.title || 'Untitled';
    }

    function pendingTitleFor(pending: PendingJoin): string {
        return pending.title || 'Pending request';
    }

    function pendingTooltipFor(pending: PendingJoin): string {
        const error = pending.lastError.trim();
        return error ? `Waiting for approval - ${error}` : 'Waiting for admin approval';
    }

    function driveActionKey(channel: DriveChannel): string {
        return `drive:${channel.id}`;
    }

    function pendingActionKey(pending: PendingJoin): string {
        return `pending:${pending.inviteHash}`;
    }

    function isActiveDrive(channel: DriveChannel): boolean {
        return $sidebarState.virtualView === null && $sidebarState.activeChannelId === channel.id;
    }

    function requestBelow(trigger: HTMLElement): SidebarActionMenuRequest {
        const rect = trigger.getBoundingClientRect();
        return { x: rect.left, y: rect.bottom + 4 };
    }

    function requestFromContextMenu(
        event: MouseEvent,
        returnTarget: HTMLElement,
    ): SidebarActionMenuRequest {
        if (event.clientX !== 0 || event.clientY !== 0) {
            return { x: event.clientX, y: event.clientY };
        }
        return requestBelow(returnTarget);
    }

    function openActions<T>(
        key: string,
        returnTarget: HTMLElement,
        request: SidebarActionMenuRequest,
        value: T,
        handler: ActionMenuHandler<T>,
    ): void {
        activeActionKey = key;
        returnFocusTo = returnTarget;
        menuOpenedByThisList = false;
        try {
            handler(request, value);
        } catch (error) {
            activeActionKey = null;
            returnFocusTo = null;
            throw error;
        }
    }

    function actionsFromButton<T>(
        event: MouseEvent,
        key: string,
        value: T,
        handler: ActionMenuHandler<T> | undefined,
    ): void {
        if (!handler) return;
        event.stopPropagation();
        const trigger = event.currentTarget as HTMLButtonElement;
        openActions(key, trigger, requestBelow(trigger), value, handler);
    }

    function returnTargetForContextMenu(row: HTMLElement, eventTarget: EventTarget | null): HTMLElement {
        const targetButton = eventTarget instanceof Element
            ? eventTarget.closest<HTMLButtonElement>('button')
            : null;
        if (targetButton && row.contains(targetButton)) return targetButton;
        return row.querySelector<HTMLButtonElement>('.drive-actions-trigger') ?? row;
    }

    function actionsFromContextMenu<T>(
        event: MouseEvent,
        key: string,
        value: T,
        handler: ActionMenuHandler<T> | undefined,
    ): void {
        if (!handler) return;
        event.preventDefault();
        const row = event.currentTarget as HTMLElement;
        const returnTarget = returnTargetForContextMenu(row, event.target);
        openActions(key, returnTarget, requestFromContextMenu(event, returnTarget), value, handler);
    }
</script>

{#if kind === 'personal'}
    {#if $sidebarState.personal.length === 0}
        <div class="drive-empty">Loading...</div>
    {:else}
        {#each $sidebarState.personal as channel (channel.id)}
            <button
                type="button"
                class="drive-item"
                class:active={isActiveDrive(channel)}
                data-channel-id={channel.id}
                title={titleFor(channel)}
                aria-current={isActiveDrive(channel) ? 'page' : undefined}
                onclick={() => onDriveClick(channel.id)}
            >
                <FolderIcon class="icon" size={18} strokeWidth={2} aria-hidden="true" />
                <span class="drive-item-title">{titleFor(channel)}</span>
            </button>
        {/each}
    {/if}
{:else if $sidebarState.shared.length === 0 && $sidebarState.pending.length === 0}
    <div class="drive-empty">No shared drives yet</div>
{:else}
    {#each $sidebarState.shared as channel (channel.id)}
        <div
            class="sidebar-drive-row"
            role="group"
            aria-label={titleFor(channel)}
            oncontextmenu={(event) => actionsFromContextMenu(event, driveActionKey(channel), channel, onDriveActions)}
        >
            <button
                type="button"
                class="drive-item"
                class:active={isActiveDrive(channel)}
                data-channel-id={channel.id}
                title={titleFor(channel)}
                aria-current={isActiveDrive(channel) ? 'page' : undefined}
                onclick={() => onDriveClick(channel.id)}
            >
                <FolderIcon class="icon" size={18} strokeWidth={2} aria-hidden="true" />
                <span class="drive-item-title">{titleFor(channel)}</span>
            </button>
            {#if onDriveActions}
                <button
                    type="button"
                    class="drive-actions-trigger"
                    aria-label={`Actions for ${titleFor(channel)}`}
                    title={`Actions for ${titleFor(channel)}`}
                    aria-haspopup="menu"
                    aria-expanded={activeActionKey === driveActionKey(channel) && $contextMenuState.open ? 'true' : 'false'}
                    onclick={(event) => actionsFromButton(event, driveActionKey(channel), channel, onDriveActions)}
                >
                    <EllipsisIcon size={18} strokeWidth={2} aria-hidden="true" />
                </button>
            {/if}
        </div>
    {/each}

    {#each $sidebarState.pending as pending (pending.inviteHash)}
        <div
            class="sidebar-drive-row"
            role="group"
            aria-label={pendingTitleFor(pending)}
            oncontextmenu={(event) => actionsFromContextMenu(event, pendingActionKey(pending), pending, onPendingActions)}
        >
            <button
                type="button"
                class="drive-item pending-drive-item"
                data-invite-hash={pending.inviteHash}
                title={pendingTooltipFor(pending)}
                onclick={() => onPendingClick?.(pending.inviteHash)}
            >
                <ClockIcon class="icon" size={18} strokeWidth={2} aria-hidden="true" />
                <span class="drive-item-title">{pendingTitleFor(pending)}</span>
                <span class="pending-drive-tag">pending</span>
            </button>
            {#if onPendingActions}
                <button
                    type="button"
                    class="drive-actions-trigger"
                    aria-label={`Actions for ${pendingTitleFor(pending)}`}
                    title={`Actions for ${pendingTitleFor(pending)}`}
                    aria-haspopup="menu"
                    aria-expanded={activeActionKey === pendingActionKey(pending) && $contextMenuState.open ? 'true' : 'false'}
                    onclick={(event) => actionsFromButton(event, pendingActionKey(pending), pending, onPendingActions)}
                >
                    <EllipsisIcon size={18} strokeWidth={2} aria-hidden="true" />
                </button>
            {/if}
        </div>
    {/each}
{/if}

<style>
    .sidebar-drive-row {
        display: grid;
        grid-template-columns: minmax(0, 1fr) 32px;
        align-items: center;
        min-width: 0;
    }

    .sidebar-drive-row > .drive-item {
        min-width: 0;
    }

    .drive-actions-trigger {
        display: inline-grid;
        width: 32px;
        height: 32px;
        margin-right: 4px;
        padding: 0;
        place-items: center;
        border: 0;
        border-radius: var(--radius-sm);
        color: var(--text-muted);
        background: transparent;
        cursor: pointer;
    }

    .drive-actions-trigger:hover {
        color: var(--text-main);
        background: var(--surface-control);
    }

    .drive-actions-trigger:focus-visible {
        outline: 2px solid var(--accent);
        outline-offset: -2px;
    }

    .drive-actions-trigger[aria-expanded='true'] {
        color: var(--accent);
        background: var(--overlay-accent-1);
    }
</style>
