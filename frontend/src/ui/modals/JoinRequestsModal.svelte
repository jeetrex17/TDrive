<script lang="ts">
    import ModalShell from './ModalShell.svelte';
    import {
        joinRequestsList,
        joinRequestsModal,
        type JoinRequestRow,
    } from './join-requests-modal-store';

    interface Props {
        onAction: (userId: number, approved: boolean) => void | Promise<void>;
    }

    let { onAction }: Props = $props();

    const view = joinRequestsModal.state;
    const list = joinRequestsList;
    const subtitle = $derived(`Pending requests for ${$view.payload?.title ?? 'this drive'}.`);

    function close(): void {
        joinRequestsModal.close();
    }

    function detailFor(row: JoinRequestRow): string {
        const pieces: string[] = [];
        if (row.username) pieces.push(row.username);
        if (row.requestedAt) pieces.push(new Date(row.requestedAt * 1000).toLocaleString());
        return pieces.join(' · ');
    }
</script>

<ModalShell
    hostId="join-requests-modal"
    open={$view.open}
    title="Join requests"
    titleId="join-requests-title"
    {subtitle}
    initialFocus="#join-requests-close"
    restoreFocus="#drives-nav"
    onClose={close}
>
    <div id="join-requests-list" class="join-requests-list">
        {#if $list.status === 'loading'}
            <div class="modal-empty">Loading requests...</div>
        {:else if $list.status === 'error'}
            <div class="modal-error">Failed to load requests: {$list.message}</div>
        {:else if $list.rows.length === 0}
            <div class="modal-empty">No pending requests.</div>
        {:else}
            {#each $list.rows as row (row.userId)}
                <div class="join-request-row">
                    <div class="join-request-meta">
                        <div class="join-request-name">{row.displayName}</div>
                        <div class="join-request-detail">{detailFor(row)}</div>
                    </div>
                    <div class="join-request-actions">
                        <button
                            type="button"
                            class="secondary-btn compact-btn"
                            disabled={$list.actingUserId !== 0}
                            onclick={() => void onAction(row.userId, true)}
                        >
                            Approve
                        </button>
                        <button
                            type="button"
                            class="secondary-btn compact-btn danger-text"
                            disabled={$list.actingUserId !== 0}
                            onclick={() => void onAction(row.userId, false)}
                        >
                            Reject
                        </button>
                    </div>
                </div>
            {/each}
        {/if}
    </div>

    {#snippet actions()}
        <button id="join-requests-close" class="secondary-btn" type="button" onclick={close}>Close</button>
    {/snippet}
</ModalShell>
