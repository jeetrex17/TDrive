<script lang="ts">
    import { onDestroy } from 'svelte';
    import { readable } from 'svelte/store';
    import { isMobilePlatform } from '../../api';
    import XIcon from '@lucide/svelte/icons/x';
    import ArrowLeftIcon from '@lucide/svelte/icons/arrow-left';
    import SearchIcon from '@lucide/svelte/icons/search';
    import ArrowUpIcon from '@lucide/svelte/icons/arrow-up';
    import ArrowDownIcon from '@lucide/svelte/icons/arrow-down';
    import ClockIcon from '@lucide/svelte/icons/clock';
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
    import { sidebarState } from '../sidebar/sidebar-store';
    import { historyEvents, type NoticeEvent } from './notif-store';
    import { visibleHistory } from './visible-history';
    import { uploadActivities, uploadsPaused, clearUploadActivityHistory } from './upload-activity-store';
    import { activityView, closeActivity, type ActivityTab } from './activity-store';
    import { ACTIVITY_FILTERS, activityEntries, activityTimestamp, isBatchPaused, matchesActivity, type ActivityEntry, type ActivityFilter } from './activity-model';
    import { formatAge, transferPercent } from './transfer-view';

    let tab = $state<ActivityTab>('transfers');
    let filter = $state<ActivityFilter>('All');
    let query = $state('');
    let selectedId = $state<string | null>(null);
    let showingDetails = $state(false);
    let confirmingClear = $state(false);
    let listPage = $state(0);
    let lastRequest: unknown = null;
    const PAGE_SIZE = 50;
    // Ages in the list ("2 min ago") move on even when nothing else does.
    const clock = readable(Date.now(), (set) => {
        const timer = setInterval(() => set(Date.now()), 30_000);
        return () => clearInterval(timer);
    });
    const activeDriveId = $derived($sidebarState.activeChannelId);
    const entries = $derived(activityEntries($uploadActivities, $visibleHistory, $resumableUploads, $resumableDownloads,
        { allEvents: $historyEvents, activeDriveId, now: $clock, uploadsPaused: $uploadsPaused }));
    const transfers = $derived(entries.filter((entry) => (filter === 'All' || entry.status === filter) && matchesActivity(entry, query)));
    const notices = $derived($historyEvents.filter((entry): entry is NoticeEvent => entry.kind === 'event' && `${entry.title} ${entry.body}`.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase())));
    // The bell's rows open a batch by its aggregate transfer id.
    const selectedTransfer = $derived(transfers.find((entry) => entry.id === selectedId || (entry.kind === 'batch' && entry.batch.transferId === selectedId)) ?? transfers[0]);
    const selectedNotice = $derived(notices.find((entry) => entry.id === selectedId) ?? notices[0]);
    const count = $derived(tab === 'transfers' ? transfers.length : notices.length);
    const listPageCount = $derived(Math.max(1, Math.ceil(count / PAGE_SIZE)));
    const currentPage = $derived(Math.min(listPage, listPageCount - 1));
    const queuedAhead = $derived.by(() => {
        if (selectedTransfer?.kind !== 'batch' || selectedTransfer.batch.status !== 'queued') return 0;
        const { batch } = selectedTransfer;
        return $uploadActivities.filter((entry) => entry.status === 'queued' && entry.driveId === batch.driveId && entry.addedAt < batch.addedAt).length;
    });
    const uploadRunning = $derived($uploadActivities.some((entry) => entry.status === 'active'));
    // Reports from a batch still listed belong to its detail page, not a second card.
    const orphanReports = $derived($failedUploadReports.filter((report) => !report.activityId || !$uploadActivities.some((entry) => entry.id === report.activityId)));
    const headline = $derived.by(() => {
        const active = entries.filter((entry) => entry.status === 'Active').length;
        const waiting = entries.filter((entry) => entry.status === 'Queued').length;
        const attention = entries.filter((entry) => entry.status === 'Needs attention').length;
        const parts = [
            active ? `${active} in progress` : '',
            waiting ? `${waiting} queued` : '',
            attention ? `${attention} ${attention === 1 ? 'needs' : 'need'} attention` : '',
        ].filter(Boolean);
        return parts.length ? parts.join(' · ') : 'Nothing in progress';
    });
    const canClear = $derived($historyEvents.some((entry) => entry.kind === 'event' || !['queued', 'active', 'paused', 'canceling'].includes(entry.status))
        || $uploadActivities.some((entry) => entry.status !== 'queued' && entry.status !== 'active'));

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
        confirmingClear = false;
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
        confirmingClear = false;
    }
    function tabKeydown(event: KeyboardEvent): void {
        if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight' && event.key !== 'Home' && event.key !== 'End') return;
        event.preventDefault();
        selectTab(event.key === 'Home' ? 'transfers' : event.key === 'End' ? 'notifications' : tab === 'transfers' ? 'notifications' : 'transfers');
        document.getElementById(`activity-tab-${tab}`)?.focus();
    }
    function entryPercent(entry: ActivityEntry): number | null {
        if (entry.status !== 'Active' || (entry.kind !== 'batch' && entry.kind !== 'transfer') || !entry.transfer) return null;
        return transferPercent(entry.transfer);
    }
    function noticeSummary(notice: NoticeEvent): string {
        const age = formatAge(notice.ts, $clock);
        const repeats = (notice.repeats ?? 1) > 1 ? `×${notice.repeats}` : '';
        return [notice.body.split('\n')[0], repeats, age].filter(Boolean).join(' · ');
    }
</script>

<ModalShell hostId="activity-modal" open={$activityView.open} titleId="activity-title" cardClass="activity-card"
    presentation="dialog" initialFocus={isMobilePlatform() ? '.close-activity' : '#activity-search'} restoreFocus={$activityView.restoreFocus} onClose={dismiss}>
    {#snippet header()}
        <div class="activity-header">
            <div class="activity-heading"><h2 id="activity-title">Activity</h2><p>{headline}</p></div>
            <div class="header-actions">
                {#if confirmingClear}
                    <span class="clear-question">Clear finished items?</span>
                    <button type="button" class="header-text-btn" onclick={() => confirmingClear = false}>Cancel</button>
                    <button type="button" class="header-text-btn danger" onclick={clear}>Clear</button>
                {:else}
                    <button type="button" class="header-text-btn" onclick={() => confirmingClear = true} disabled={!canClear}>Clear history</button>
                {/if}
                <button type="button" class="close-activity" aria-label="Close activity" onclick={closeActivity}><XIcon size={18} aria-hidden="true" /></button>
            </div>
        </div>
    {/snippet}
    <div class="activity-body" class:showing-details={showingDetails}>
        <div class="activity-toolbar">
            <div class="activity-tabs" role="tablist" tabindex="-1" aria-label="Activity type" onkeydown={tabKeydown}>
                <button type="button" id="activity-tab-transfers" role="tab" aria-selected={tab === 'transfers'} aria-controls="activity-content" tabindex={tab === 'transfers' ? 0 : -1} onclick={() => selectTab('transfers')}>Transfers</button>
                <button type="button" id="activity-tab-notifications" role="tab" aria-selected={tab === 'notifications'} aria-controls="activity-content" tabindex={tab === 'notifications' ? 0 : -1} onclick={() => selectTab('notifications')}>Notifications</button>
            </div>
            <label class="activity-search"><SearchIcon size={15} aria-hidden="true" /><input id="activity-search" type="search" aria-label="Search activity" placeholder={tab === 'transfers' ? 'Search files or destination' : 'Search notifications'} bind:value={query} oninput={() => { listPage = 0; showingDetails = false; }} /></label>
        </div>
        <div id="activity-content" role="tabpanel" aria-labelledby={`activity-tab-${tab}`} class="activity-content" class:empty={count === 0}>
            <div class="activity-list-pane">
                {#if tab === 'transfers'}
                    <div class="activity-filters" role="group" aria-label="Filter transfers">
                        {#each ACTIVITY_FILTERS as option (option)}<button type="button" aria-pressed={filter === option} onclick={() => selectFilter(option)}>{option}</button>{/each}
                    </div>
                    {#if (filter === 'All' || filter === 'Needs attention') && !query && orphanReports.length}
                        <div class="failure-reports"><FailedUploadReports reports={orphanReports} onReview={reviewFailedUploads} onDismiss={dismissFailedUploadReport} /></div>
                    {/if}
                {/if}
                {#if count === 0}
                    <div class="activity-empty"><BellIcon size={28} aria-hidden="true" /><h3>{query || filter !== 'All' ? 'No matching activity' : tab === 'transfers' ? 'No transfers yet' : 'All caught up'}</h3><p>{query || filter !== 'All' ? 'Try another search or filter.' : tab === 'transfers' ? 'Uploads and downloads will appear here.' : 'Your notifications will appear here.'}</p></div>
                {:else}
                    <ul class="activity-list" aria-label={tab === 'transfers' ? 'Transfers' : 'Notifications'}>
                        {#if tab === 'transfers'}
                            {#each transfers.slice(currentPage * PAGE_SIZE, (currentPage + 1) * PAGE_SIZE) as entry (entry.id)}
                                {@const percent = entryPercent(entry)}
                                {@const paused = entry.kind === 'batch' && isBatchPaused(entry.batch, activeDriveId)}
                                <li><button type="button" class="activity-item" class:selected={selectedTransfer?.id === entry.id} aria-label={`View ${entry.name}`} aria-pressed={selectedTransfer?.id === entry.id} onclick={() => select(entry.id)}>
                                    <span class="item-glyph" data-tone={entry.status === 'Needs attention' ? 'danger' : paused ? 'warning' : entry.status === 'Queued' ? 'muted' : 'accent'}>
                                        {#if entry.status === 'Queued'}<ClockIcon size={16} aria-hidden="true" />{:else if entry.kind === 'download' || (entry.kind === 'transfer' && entry.transfer.direction === 'down')}<ArrowDownIcon size={16} aria-hidden="true" />{:else}<ArrowUpIcon size={16} aria-hidden="true" />{/if}
                                    </span>
                                    <span class="item-copy">
                                        <span class="item-title">{entry.name}</span>
                                        <span class="item-summary" data-tone={paused ? 'warning' : entry.status === 'Needs attention' ? 'danger' : undefined}>{entry.summary}</span>
                                        {#if percent !== null}<span class="item-bar" aria-hidden="true"><span style={`width:${percent}%`}></span></span>{/if}
                                    </span>
                                </button></li>
                            {/each}
                        {:else}
                            {#each notices.slice(currentPage * PAGE_SIZE, (currentPage + 1) * PAGE_SIZE) as notice (notice.id)}
                                <li><button type="button" class="activity-item" class:selected={selectedNotice?.id === notice.id} aria-label={`View ${notice.title}`} aria-pressed={selectedNotice?.id === notice.id} onclick={() => select(notice.id)}>
                                    <span class="item-glyph" data-tone={notice.level === 'error' ? 'danger' : notice.level === 'warning' ? 'warning' : 'accent'}><BellIcon size={16} aria-hidden="true" /></span>
                                    <span class="item-copy"><span class="item-title">{notice.title}</span><span class="item-summary clamp">{noticeSummary(notice)}</span></span>
                                </button></li>
                            {/each}
                        {/if}
                    </ul>
                    {#if listPageCount > 1}<div class="list-pagination"><button type="button" aria-label="Previous page" disabled={currentPage === 0} onclick={() => listPage = currentPage - 1}>Previous</button><span>{currentPage * PAGE_SIZE + 1}–{Math.min((currentPage + 1) * PAGE_SIZE, count)} of {count}</span><button type="button" aria-label="Next page" disabled={currentPage === listPageCount - 1} onclick={() => listPage = currentPage + 1}>Next</button></div>{/if}
                {/if}
            </div>
            {#if count > 0}
                <div class="activity-detail-pane">
                    <button type="button" class="activity-back" onclick={() => showingDetails = false}><ArrowLeftIcon size={16} aria-hidden="true" /> Activity</button>
                    {#if tab === 'transfers' && selectedTransfer}
                        {#key selectedTransfer.id}<ActivityDetails entry={selectedTransfer} {activeDriveId} {queuedAhead} {uploadRunning} />{/key}
                    {:else if tab === 'notifications' && selectedNotice}
                        {#key selectedNotice.id}<section class="notice-details" aria-label={`Details for ${selectedNotice.title}`}><span class="notice-level" data-level={selectedNotice.level}>{selectedNotice.level}</span><h3>{selectedNotice.title}</h3><time datetime={new Date(selectedNotice.ts).toISOString()}>{activityTimestamp(selectedNotice.ts)}</time><ActivityMessage title={selectedNotice.title} message={selectedNotice.body} />{#if (selectedNotice.repeats ?? 1) > 1}<p>Repeated {selectedNotice.repeats} times</p>{/if}</section>{/key}
                    {/if}
                </div>
            {/if}
        </div>
    </div>
</ModalShell>

<style>
    :global(.activity-card) { max-width: 1060px; height: min(82dvh, 800px); display: flex; flex-direction: column; padding: 0; overflow: hidden; color: var(--color-text-soft); }
    .activity-header { display: flex; align-items: center; justify-content: space-between; gap: var(--space-4); padding: var(--space-5) var(--space-6) var(--space-4); }
    .activity-heading { min-width: 0; }
    h2 { margin: 0; font-size: var(--type-xl); font-weight: var(--weight-strong); }
    .activity-header p { margin: var(--space-1) 0 0; color: var(--color-text-muted); font-size: var(--type-xs); }
    .header-actions { display: flex; align-items: center; gap: var(--space-2); flex-shrink: 0; }
    button { font: inherit; cursor: pointer; }
    .close-activity { display: grid; place-items: center; width: var(--control-md); height: var(--control-md); border: 0; background: transparent; color: var(--color-text-muted); border-radius: var(--radius-sm); }
    .header-text-btn { border: 0; border-radius: var(--radius-sm); background: transparent; color: var(--color-text-muted); font-size: var(--type-xs); padding: var(--space-2); }
    .header-text-btn.danger { color: var(--color-danger); font-weight: var(--weight-semibold); }
    .clear-question { color: var(--color-text-soft); font-size: var(--type-xs); }
    button:disabled { opacity: .45; cursor: default; }
    button:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }
    .activity-body { display: flex; flex-direction: column; flex: 1; min-height: 0; }
    .activity-toolbar { display: flex; align-items: flex-end; justify-content: space-between; gap: var(--space-4); padding: 0 var(--space-6); border-bottom: 1px solid var(--color-border-soft); }
    .activity-tabs { display: flex; gap: var(--space-5); }
    .activity-tabs button { border: 0; border-bottom: 2px solid transparent; padding: var(--space-3) 0; background: transparent; color: var(--color-text-muted); font-size: var(--type-base); }
    .activity-tabs button[aria-selected='true'] { border-bottom-color: var(--color-accent); color: var(--color-text-soft); font-weight: var(--weight-semibold); }
    .activity-search { display: flex; align-items: center; gap: var(--space-2); width: min(280px, 42%); height: var(--control-md); margin-bottom: var(--space-2); box-sizing: border-box; color: var(--color-text-muted); padding: 0 var(--space-3); background: var(--color-surface-1); border: 1px solid var(--color-border-soft); border-radius: var(--radius-md); transition: border-color var(--motion-fast) var(--ease-standard); }
    .activity-search:focus-within { border-color: var(--color-accent); }
    .activity-search input { flex: 1; min-width: 0; height: 100%; margin: 0; padding: 0; border: 0; outline: none; background: transparent; color: var(--color-text-soft); font: inherit; font-size: var(--type-sm); line-height: 1; box-shadow: none; appearance: none; }
    .activity-search input::-webkit-search-cancel-button { cursor: pointer; }
    /* A list column like Mail's: wide enough for a name and its state, the rest to the detail. */
    .activity-content { display: grid; grid-template-columns: minmax(260px, 380px) minmax(0, 1fr); min-height: 0; flex: 1; }
    .activity-content.empty { grid-template-columns: 1fr; }
    .activity-list-pane, .activity-detail-pane { overflow: auto; min-width: 0; overscroll-behavior: contain; }
    .activity-list-pane { border-right: 1px solid var(--color-border-soft); background: var(--color-surface-0); }
    .empty .activity-list-pane { border-right: 0; }
    .activity-detail-pane { background: var(--color-surface-1); }
    /* Wraps rather than scrolls: a narrow pane must never cut a chip off at its edge. */
    .activity-filters { display: flex; flex-wrap: wrap; gap: var(--space-0-5); padding: var(--space-3) var(--space-2) var(--space-1); }
    .activity-filters button { flex-shrink: 0; border: 1px solid transparent; border-radius: var(--radius-pill); padding: var(--space-1) var(--space-2-5); background: transparent; color: var(--color-text-muted); font-size: var(--type-xs); white-space: nowrap; }
    .activity-filters button[aria-pressed='true'] { background: var(--overlay-accent-1); border-color: var(--overlay-accent-2); color: var(--color-accent-text); }
    .activity-list { margin: 0; padding: var(--space-2); list-style: none; }
    .activity-item { display: flex; align-items: flex-start; width: 100%; gap: var(--space-3); padding: var(--space-3); border: 1px solid transparent; border-radius: var(--radius-md); background: transparent; color: var(--color-text-soft); text-align: left; }
    .activity-item.selected { background: var(--overlay-accent-1); border-color: var(--overlay-accent-2); }
    .item-glyph { display: grid; place-items: center; color: var(--color-accent); padding-top: 2px; }
    .item-glyph[data-tone='danger'] { color: var(--color-danger); }
    .item-glyph[data-tone='warning'] { color: var(--color-warning); }
    .item-glyph[data-tone='muted'] { color: var(--color-text-muted); }
    .item-copy { display: grid; gap: var(--space-1); min-width: 0; flex: 1; }
    .item-title { font-size: var(--type-sm); font-weight: var(--weight-semibold); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .item-summary { color: var(--color-text-muted); font-size: var(--type-xs); line-height: var(--leading-normal); overflow-wrap: anywhere; }
    .item-summary[data-tone='warning'] { color: var(--color-warning); }
    .item-summary[data-tone='danger'] { color: var(--color-danger); }
    .item-summary.clamp { display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
    .item-bar { display: block; height: 3px; margin-top: var(--space-1); border-radius: var(--radius-pill); background: var(--overlay-neutral-2); overflow: hidden; }
    .item-bar span { display: block; height: 100%; border-radius: inherit; background: var(--color-accent); transition: width 200ms ease-out; }
    .activity-empty { display: grid; justify-items: center; gap: var(--space-2); padding: var(--space-8) var(--space-6); text-align: center; color: var(--color-text-muted); font-size: var(--type-sm); }
    .activity-empty h3 { margin: var(--space-2) 0 0; color: var(--color-text-soft); font-size: var(--type-base); font-weight: var(--weight-semibold); }
    .activity-empty p { margin: 0; font-size: var(--type-xs); line-height: var(--leading-normal); }
    .failure-reports { padding: var(--space-2) var(--space-3) 0; }
    .notice-details { padding: var(--space-6); }
    .notice-details h3 { margin: var(--space-2) 0; font-size: var(--type-xl); overflow-wrap: anywhere; }
    .notice-level { text-transform: capitalize; font-size: var(--type-xs); color: var(--color-text-muted); }
    .notice-level[data-level='error'] { color: var(--color-danger); }
    .notice-level[data-level='warning'] { color: var(--color-warning); }
    time, .notice-details > p { color: var(--color-text-muted); font-size: var(--type-xs); }
    .list-pagination { display: flex; align-items: center; justify-content: space-between; gap: var(--space-2); padding: var(--space-2) var(--space-3) var(--space-4); font-size: var(--type-xs); color: var(--color-text-muted); }
    .list-pagination button { border: 0; padding: var(--space-2); background: transparent; color: var(--color-accent-text); }
    .activity-back { display: none; }
    @media (prefers-reduced-motion: reduce) { .item-bar span, .activity-search { transition: none; } }
    @media (hover: hover) {
        .activity-item:not(.selected):hover, .close-activity:hover, .header-text-btn:not(:disabled):hover { background: var(--overlay-neutral-1); }
        .activity-filters button:not([aria-pressed='true']):hover { color: var(--color-text-soft); }
    }
    @media (max-width: 700px) {
        :global(#activity-modal) { padding: 0; }
        :global(.activity-card) { height: calc(100dvh - var(--inset-top) - var(--inset-keyboard)); max-height: none; margin-top: var(--inset-top); border-radius: 0; border: 0; padding-bottom: calc(var(--inset-bottom) - var(--inset-keyboard)); }
        .activity-header { padding: var(--space-3) var(--space-2) var(--space-3) var(--space-4); }
        .header-actions { gap: 0; }
        .clear-question { display: none; }
        .close-activity { width: var(--touch-target); height: var(--touch-target); }
        .header-text-btn { min-height: var(--touch-target); padding-inline: var(--space-3); }
        .activity-toolbar { flex-direction: column; align-items: stretch; gap: var(--space-3); padding: 0 var(--space-4) var(--space-3); }
        .activity-tabs button { min-height: var(--touch-target); }
        .activity-search { width: 100%; height: var(--touch-target); margin: 0; }
        .activity-search input { font-size: 16px; }
        .activity-filters { padding: var(--space-3) var(--space-4) var(--space-1); }
        .activity-filters button { min-height: 36px; padding-inline: var(--space-3); font-size: var(--type-sm); }
        .activity-content { display: block; }
        .activity-list-pane, .activity-detail-pane { height: 100%; }
        .activity-list-pane { border: 0; }
        .activity-detail-pane { display: none; }
        .showing-details .activity-toolbar, .showing-details .activity-list-pane { display: none; }
        .showing-details .activity-detail-pane { display: block; }
        .activity-back { display: flex; align-items: center; gap: var(--space-2); min-height: var(--touch-target); margin: var(--space-1) var(--space-2) 0; padding: 0 var(--space-2); border: 0; background: transparent; color: var(--color-accent-text); font-size: var(--type-sm); }
        .notice-details { padding: var(--space-4); }
        .activity-item { padding-block: var(--space-4); }
    }
</style>
