<script lang="ts">
    import { onDestroy } from 'svelte';
    import { isMobilePlatform } from '../../api';
    import XIcon from '@lucide/svelte/icons/x';
    import ArrowLeftIcon from '@lucide/svelte/icons/arrow-left';
    import SearchIcon from '@lucide/svelte/icons/search';
    import ArrowUpIcon from '@lucide/svelte/icons/arrow-up';
    import ArrowDownIcon from '@lucide/svelte/icons/arrow-down';
    import BellIcon from '@lucide/svelte/icons/bell';
    import ModalShell from '../modals/ModalShell.svelte';
    import ActivityDetails from './ActivityDetails.svelte';
    import ActivityMessage from './ActivityMessage.svelte';
    import FailedUploadReports from './FailedUploadReports.svelte';
    import { dismissFailedUploadReport, failedUploadReports } from '../modals/failed-uploads-store';
    import { reviewFailedUploads } from '../../modules/transfers';
    import { clearHistory } from '../../modules/notif-bell';
    import { resumableUploads } from '../../modules/resumable-upload-store';
    import { resumableDownloads } from '../../modules/resumable-download-store';
    import { historyEvents, type NoticeEvent } from './notif-store';
    import { visibleHistory } from './visible-history';
    import { uploadActivities, clearUploadActivityHistory } from './upload-activity-store';
    import { activityView, closeActivity, type ActivityTab } from './activity-store';
    import { ACTIVITY_FILTERS, activityEntries, activityTimestamp, matchesActivity, type ActivityFilter } from './activity-model';

    let tab = $state<ActivityTab>('transfers');
    let filter = $state<ActivityFilter>('All');
    let query = $state('');
    let selectedId = $state<string | null>(null);
    let showingDetails = $state(false);
    let listPage = $state(0);
    let lastRequest: unknown = null;
    const PAGE_SIZE = 50;
    const entries = $derived(activityEntries($uploadActivities, $visibleHistory, $resumableUploads, $resumableDownloads, $historyEvents));
    const transfers = $derived(entries.filter((entry) => (filter === 'All' || entry.status === filter) && matchesActivity(entry, query)));
    const notices = $derived($historyEvents.filter((entry): entry is NoticeEvent => entry.kind === 'event' && `${entry.title} ${entry.body}`.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase())));
    const selectedTransfer = $derived(transfers.find((entry) => entry.id === selectedId) ?? transfers[0]);
    const selectedNotice = $derived(notices.find((entry) => entry.id === selectedId) ?? notices[0]);
    const count = $derived(tab === 'transfers' ? transfers.length : notices.length);
    const listPageCount = $derived(Math.max(1, Math.ceil(count / PAGE_SIZE)));
    const currentPage = $derived(Math.min(listPage, listPageCount - 1));
    const queued = $derived($uploadActivities.filter((entry) => entry.status === 'queued').sort((a, b) => a.addedAt - b.addedAt));
    const queuedPosition = $derived(selectedTransfer ? queued.findIndex((entry) => entry.id === selectedTransfer.id) + 1 : 0);
    const activeCount = $derived(entries.filter((entry) => entry.status === 'Active').length);
    const waitingCount = $derived(entries.filter((entry) => entry.status === 'Queued').length);
    const attentionCount = $derived(entries.filter((entry) => entry.status === 'Needs attention').length);

    $effect(() => {
        const request = $activityView;
        if (request === lastRequest || !request.open) return;
        lastRequest = request;
        tab = request.tab;
        selectedId = request.selectedId;
        showingDetails = Boolean(request.selectedId);
        filter = 'All';
        query = '';
        listPage = 0;
    });

    onDestroy(closeActivity);
    function dismiss(): void {
        if (isMobilePlatform() && showingDetails) { showingDetails = false; return; }
        closeActivity();
    }
    function selectTab(next: ActivityTab): void {
        tab = next; selectedId = null; showingDetails = false; listPage = 0;
    }
    function select(id: string): void { selectedId = id; showingDetails = true; }
    function selectFilter(next: ActivityFilter): void { filter = next; showingDetails = false; listPage = 0; }
    function clear(): void {
        clearHistory();
        clearUploadActivityHistory();
    }
    function tabKeydown(event: KeyboardEvent): void {
        if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight' && event.key !== 'Home' && event.key !== 'End') return;
        event.preventDefault();
        selectTab(event.key === 'Home' ? 'transfers' : event.key === 'End' ? 'notifications' : tab === 'transfers' ? 'notifications' : 'transfers');
        document.getElementById(`activity-tab-${tab}`)?.focus();
    }
</script>

<ModalShell hostId="activity-modal" open={$activityView.open} titleId="activity-title" cardClass="activity-card"
    presentation="dialog" initialFocus="#activity-search" restoreFocus={$activityView.restoreFocus} onClose={dismiss}>
    {#snippet header()}
        <div class="activity-header">
            <div><h2 id="activity-title">Activity</h2><p>{activeCount} active · {waitingCount} queued · {attentionCount} need attention</p></div>
            <div class="header-actions">
                <button type="button" class="clear-history" onclick={clear} disabled={$historyEvents.length === 0 && !$uploadActivities.some((entry) => entry.status !== 'queued' && entry.status !== 'active')}>Clear history</button>
                <button type="button" class="close-activity" aria-label="Close activity" onclick={closeActivity}><XIcon size={18} aria-hidden="true" /></button>
            </div>
        </div>
    {/snippet}
    <div class="activity-toolbar">
        <div class="activity-tabs" role="tablist" tabindex="-1" aria-label="Activity type" onkeydown={tabKeydown}>
            <button type="button" id="activity-tab-transfers" role="tab" aria-selected={tab === 'transfers'} aria-controls="activity-content" tabindex={tab === 'transfers' ? 0 : -1} onclick={() => selectTab('transfers')}>Transfers</button>
            <button type="button" id="activity-tab-notifications" role="tab" aria-selected={tab === 'notifications'} aria-controls="activity-content" tabindex={tab === 'notifications' ? 0 : -1} onclick={() => selectTab('notifications')}>Notifications</button>
        </div>
        <label class="activity-search"><SearchIcon size={15} aria-hidden="true" /><input id="activity-search" type="search" aria-label="Search activity" placeholder={tab === 'transfers' ? 'Search files or destination' : 'Search notifications'} bind:value={query} oninput={() => { listPage = 0; showingDetails = false; }} /></label>
    </div>
    {#if tab === 'transfers'}
        <div class="activity-filters" role="group" aria-label="Filter transfers">
            {#each ACTIVITY_FILTERS as option (option)}<button type="button" aria-pressed={filter === option} onclick={() => selectFilter(option)}>{option}</button>{/each}
        </div>
    {/if}
    <div id="activity-content" role="tabpanel" aria-labelledby={`activity-tab-${tab}`} class="activity-content" class:showing-details={showingDetails}>
        <div class="activity-list-pane">
            {#if tab === 'transfers' && (filter === 'All' || filter === 'Needs attention') && !query}
                <div class="failure-reports"><FailedUploadReports reports={$failedUploadReports} onReview={reviewFailedUploads} onDismiss={dismissFailedUploadReport} /></div>
            {/if}
            {#if count === 0}
                <div class="activity-empty"><BellIcon size={28} aria-hidden="true" /><h3>{query || filter !== 'All' ? 'No matching activity' : tab === 'transfers' ? 'No transfers yet' : 'All caught up'}</h3><p>{query || filter !== 'All' ? 'Try another search or filter.' : tab === 'transfers' ? 'Uploads and downloads will appear here.' : 'Your notifications will appear here.'}</p></div>
            {:else}
                <ul class="activity-list" aria-label={tab === 'transfers' ? 'Transfers' : 'Notifications'}>
                    {#if tab === 'transfers'}
                        {#each transfers.slice(currentPage * PAGE_SIZE, (currentPage + 1) * PAGE_SIZE) as entry (entry.id)}
                            <li><button type="button" class="activity-item" class:selected={selectedTransfer?.id === entry.id} aria-label={`View ${entry.name}`} aria-pressed={selectedTransfer?.id === entry.id} onclick={() => select(entry.id)}>
                                <span class="item-glyph" data-attention={entry.status === 'Needs attention'}>{#if entry.kind === 'download' || (entry.kind === 'transfer' && entry.transfer.direction === 'down')}<ArrowDownIcon size={17} aria-hidden="true" />{:else}<ArrowUpIcon size={17} aria-hidden="true" />{/if}</span>
                                <span class="item-copy"><span class="item-title">{entry.name}</span><span class="item-summary">{entry.summary}</span></span>
                            </button></li>
                        {/each}
                    {:else}
                        {#each notices.slice(currentPage * PAGE_SIZE, (currentPage + 1) * PAGE_SIZE) as notice (notice.id)}
                            <li><button type="button" class="activity-item" class:selected={selectedNotice?.id === notice.id} aria-label={`View ${notice.title}`} aria-pressed={selectedNotice?.id === notice.id} onclick={() => select(notice.id)}>
                                <span class="item-glyph" data-attention={notice.level === 'error'}><BellIcon size={17} aria-hidden="true" /></span>
                                <span class="item-copy"><span class="item-title">{notice.title}</span><span class="item-summary">{notice.level} · {activityTimestamp(notice.ts)}</span></span>
                            </button></li>
                        {/each}
                    {/if}
                </ul>
                {#if listPageCount > 1}<div class="list-pagination"><button type="button" disabled={currentPage === 0} onclick={() => listPage = currentPage - 1}>Previous activity</button><span>{currentPage + 1} / {listPageCount}</span><button type="button" disabled={currentPage === listPageCount - 1} onclick={() => listPage = currentPage + 1}>Next activity</button></div>{/if}
            {/if}
        </div>
        <div class="activity-detail-pane">
            <button type="button" class="activity-back" onclick={() => showingDetails = false}><ArrowLeftIcon size={16} aria-hidden="true" /> Back to activity</button>
            {#if tab === 'transfers' && selectedTransfer}
                {#key selectedTransfer.id}<ActivityDetails entry={selectedTransfer} queuePosition={queuedPosition} />{/key}
            {:else if tab === 'notifications' && selectedNotice}
                {#key selectedNotice.id}<section class="notice-details" aria-label={`Details for ${selectedNotice.title}`}><span class="notice-level">{selectedNotice.level}</span><h3>{selectedNotice.title}</h3><time datetime={new Date(selectedNotice.ts).toISOString()}>{activityTimestamp(selectedNotice.ts)}</time><ActivityMessage title={selectedNotice.title} message={selectedNotice.body} />{#if (selectedNotice.repeats ?? 1) > 1}<p>Repeated {selectedNotice.repeats} times</p>{/if}</section>{/key}
            {:else}
                <div class="detail-placeholder"><p>Select activity to see its details.</p></div>
            {/if}
        </div>
    </div>
</ModalShell>

<style>
    :global(.activity-card) { max-width: 1060px; height: min(82dvh, 800px); display: flex; flex-direction: column; padding: 0; overflow: hidden; color: var(--color-text-soft); }
    .activity-header { display: flex; align-items: center; justify-content: space-between; gap: var(--space-4); padding: var(--space-5) var(--space-6); }
    h2 { margin: 0; font-size: var(--type-xl); font-weight: var(--weight-strong); }
    .activity-header p { margin: var(--space-1) 0 0; color: var(--color-text-muted); font-size: var(--type-xs); }
    .header-actions { display: flex; align-items: center; gap: var(--space-3); }
    button { font: inherit; cursor: pointer; }
    .close-activity { display: grid; place-items: center; width: var(--control-md); height: var(--control-md); border: 0; background: transparent; color: var(--color-text-muted); border-radius: var(--radius-sm); }
    .clear-history { border: 0; background: transparent; color: var(--color-text-muted); font-size: var(--type-xs); padding: var(--space-2); }
    button:disabled { opacity: .45; cursor: default; }
    button:focus-visible, input:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }
    .activity-toolbar { display: flex; align-items: center; justify-content: space-between; gap: var(--space-4); padding: 0 var(--space-6); border-bottom: 1px solid var(--color-border-soft); }
    .activity-tabs { display: flex; align-self: stretch; gap: var(--space-5); }
    .activity-tabs button { border: 0; border-bottom: 2px solid transparent; padding: var(--space-3) 0; background: transparent; color: var(--color-text-muted); font-size: var(--type-base); }
    .activity-tabs button[aria-selected='true'] { border-bottom-color: var(--color-accent); color: var(--color-text-soft); font-weight: var(--weight-semibold); }
    .activity-search { display: flex; align-items: center; gap: var(--space-2); width: min(300px, 42%); color: var(--color-text-muted); padding: var(--space-2) var(--space-3); background: var(--color-surface-1); border: 1px solid var(--color-border-soft); border-radius: var(--radius-md); }
    input { min-width: 0; width: 100%; padding: 0; border: 0; background: transparent; color: var(--color-text-soft); font: inherit; font-size: var(--type-xs); }
    .activity-filters { display: flex; flex-wrap: wrap; gap: var(--space-1); padding: var(--space-3) var(--space-6); border-bottom: 1px solid var(--color-border-soft); }
    .activity-filters button { border: 0; border-radius: var(--radius-sm); padding: var(--space-2) var(--space-3); background: transparent; color: var(--color-text-muted); font-size: var(--type-xs); }
    .activity-filters button[aria-pressed='true'] { background: var(--overlay-accent-1); color: var(--color-accent-text); }
    .activity-content { display: grid; grid-template-columns: minmax(260px, .85fr) minmax(0, 1.4fr); min-height: 0; flex: 1; }
    .activity-list-pane, .activity-detail-pane { overflow: auto; min-width: 0; overscroll-behavior: contain; }
    .activity-list-pane { border-right: 1px solid var(--color-border-soft); background: var(--color-surface-0); }
    .activity-detail-pane { background: var(--color-surface-1); }
    .activity-list { margin: 0; padding: var(--space-2); list-style: none; }
    .activity-item { display: flex; align-items: flex-start; width: 100%; gap: var(--space-3); padding: var(--space-3); border: 1px solid transparent; border-radius: var(--radius-md); background: transparent; color: var(--color-text-soft); text-align: left; }
    .activity-item.selected { background: var(--overlay-accent-1); border-color: var(--overlay-accent-2); }
    .item-glyph { display: grid; place-items: center; color: var(--color-accent); padding-top: var(--space-0-5); }
    .item-glyph[data-attention='true'] { color: var(--color-danger); }
    .item-copy { display: grid; gap: var(--space-1); min-width: 0; }
    .item-title { font-size: var(--type-sm); font-weight: var(--weight-semibold); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .item-summary { color: var(--color-text-muted); font-size: var(--type-xs); line-height: var(--leading-normal); overflow-wrap: anywhere; }
    .activity-empty, .detail-placeholder { padding: var(--space-8) var(--space-6); text-align: center; color: var(--color-text-muted); font-size: var(--type-sm); }
    .activity-empty h3 { color: var(--color-text-soft); font-size: var(--type-base); font-weight: var(--weight-semibold); }
    .activity-empty p { font-size: var(--type-xs); line-height: var(--leading-normal); }
    .failure-reports { padding: var(--space-3); }
    .notice-details { padding: var(--space-6); }
    .notice-details h3 { margin: var(--space-2) 0; font-size: var(--type-xl); overflow-wrap: anywhere; }
    .notice-level { text-transform: capitalize; font-size: var(--type-xs); color: var(--color-text-muted); }
    time, .notice-details > p { color: var(--color-text-muted); font-size: var(--type-xs); }
    .list-pagination { display: flex; align-items: center; justify-content: space-between; gap: var(--space-2); padding: var(--space-3); font-size: var(--type-xs); color: var(--color-text-muted); }
    .list-pagination button { border: 0; background: transparent; color: var(--color-accent-text); }
    .activity-back { display: none; }
    @media (hover: hover) { .activity-item:hover { background: var(--overlay-neutral-1); } .close-activity:hover { background: var(--overlay-neutral-1); } }
    @media (max-width: 700px) {
        :global(#activity-modal) { padding: 0; }
        :global(.activity-card) { height: calc(100dvh - var(--inset-top) - var(--inset-keyboard)); max-height: none; margin-top: var(--inset-top); border-radius: 0; border: 0; padding-bottom: calc(var(--inset-bottom) - var(--inset-keyboard)); }
        .activity-header { padding: var(--space-4); }
        .header-actions { gap: var(--space-1); }
        .close-activity { width: var(--touch-target); height: var(--touch-target); }
        .clear-history { min-height: var(--touch-target); }
        .activity-toolbar { padding: 0 var(--space-4) var(--space-3); flex-wrap: wrap; gap: var(--space-2); }
        .activity-tabs { gap: var(--space-5); }
        .activity-tabs button { min-height: var(--touch-target); }
        .activity-search { width: 100%; min-height: var(--touch-target); box-sizing: border-box; }
        .activity-filters { padding: var(--space-2) var(--space-3); gap: 0; }
        .activity-filters button { min-height: var(--touch-target); padding-inline: var(--space-2); }
        .activity-content { display: block; }
        .activity-list-pane, .activity-detail-pane { height: 100%; }
        .activity-list-pane { border: 0; }
        .activity-detail-pane { display: none; }
        .showing-details .activity-list-pane { display: none; }
        .showing-details .activity-detail-pane { display: block; }
        .activity-back { display: flex; align-items: center; gap: var(--space-2); min-height: var(--touch-target); margin: var(--space-2) var(--space-4) 0; border: 0; background: transparent; color: var(--color-accent-text); font-size: var(--type-sm); }
        .notice-details { padding: var(--space-4); }
        .activity-item { padding-block: var(--space-4); }
    }
</style>
