<script lang="ts">
    import { sidebarState, type SidebarChannel, type SidebarPendingJoin } from './sidebar-store';

    type DriveKind = 'personal' | 'shared';

    interface Props {
        kind: DriveKind;
        onDriveClick: (channelId: number) => void;
        onDriveContextMenu?: (event: MouseEvent, channel: SidebarChannel) => void;
        onPendingClick?: (inviteHash: string) => void;
        onPendingContextMenu?: (event: MouseEvent, pending: SidebarPendingJoin) => void;
    }

    let {
        kind,
        onDriveClick,
        onDriveContextMenu,
        onPendingClick,
        onPendingContextMenu,
    }: Props = $props();

    const folderPath = 'M3 7a2 2 0 012-2h5l2 2h7a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2V7z';
    const pendingPath = 'M12 8v4l3 2m6-2a9 9 0 11-18 0 9 9 0 0118 0z';

    function titleFor(channel: SidebarChannel): string {
        return channel.title || 'Untitled';
    }

    function pendingTitleFor(pending: SidebarPendingJoin): string {
        return pending.title || 'Pending request';
    }

    function pendingTooltipFor(pending: SidebarPendingJoin): string {
        const error = String(pending.last_error || '').trim();
        return error ? `Waiting for approval - ${error}` : 'Waiting for admin approval';
    }

    function contextMenuForDrive(event: MouseEvent, channel: SidebarChannel): void {
        if (!onDriveContextMenu) return;
        event.preventDefault();
        onDriveContextMenu(event, channel);
    }

    function contextMenuForPending(event: MouseEvent, pending: SidebarPendingJoin): void {
        if (!onPendingContextMenu) return;
        event.preventDefault();
        onPendingContextMenu(event, pending);
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
                class:active={channel.is_active && !$sidebarState.photosActive}
                data-channel-id={channel.id}
                title={titleFor(channel)}
                onclick={() => onDriveClick(channel.id)}
            >
                <svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                    <path stroke-linecap="round" stroke-linejoin="round" d={folderPath} />
                </svg>
                <span class="drive-item-title">{titleFor(channel)}</span>
            </button>
        {/each}
    {/if}
{:else if $sidebarState.shared.length === 0 && $sidebarState.pending.length === 0}
    <div class="drive-empty">No shared drives yet</div>
{:else}
    {#each $sidebarState.shared as channel (channel.id)}
        <button
            type="button"
            class="drive-item"
            class:active={channel.is_active && !$sidebarState.photosActive}
            data-channel-id={channel.id}
            title={titleFor(channel)}
            onclick={() => onDriveClick(channel.id)}
            oncontextmenu={(event) => contextMenuForDrive(event, channel)}
        >
            <svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <path stroke-linecap="round" stroke-linejoin="round" d={folderPath} />
            </svg>
            <span class="drive-item-title">{titleFor(channel)}</span>
        </button>
    {/each}

    {#each $sidebarState.pending as pending (pending.invite_hash)}
        <button
            type="button"
            class="drive-item pending-drive-item"
            data-invite-hash={pending.invite_hash}
            title={pendingTooltipFor(pending)}
            onclick={() => onPendingClick?.(pending.invite_hash)}
            oncontextmenu={(event) => contextMenuForPending(event, pending)}
        >
            <svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <path stroke-linecap="round" stroke-linejoin="round" d={pendingPath} />
            </svg>
            <span class="drive-item-title">{pendingTitleFor(pending)}</span>
            <span class="pending-drive-tag">pending</span>
        </button>
    {/each}
{/if}
