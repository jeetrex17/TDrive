<script lang="ts">
    import { onMount, tick, untrack } from 'svelte';
    import ChevronLeftIcon from '@lucide/svelte/icons/chevron-left';
    import EllipsisIcon from '@lucide/svelte/icons/ellipsis';
    import ExternalLinkIcon from '@lucide/svelte/icons/external-link';
    import LockIcon from '@lucide/svelte/icons/lock';
    import EyeIcon from '@lucide/svelte/icons/eye';
    import PlayIcon from '@lucide/svelte/icons/play';
    import RadioTowerIcon from '@lucide/svelte/icons/radio-tower';
    import SearchIcon from '@lucide/svelte/icons/search';
    import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';
    import XIcon from '@lucide/svelte/icons/x';
    import { setScreenProtect } from '../../api';
    import { acquireScreenProtection, bindViewOnlyGuards } from '../viewers/view-only-guards';
    import { bindLongPress } from '../file-list/touch';
    import ChannelTopicPicker from './ChannelTopicPicker.svelte';
    import { mergeTopicMetadata, type ChannelTopic, type ChannelTopicFetcher } from './channel-topics';
    import ArrowDownUpIcon from '@lucide/svelte/icons/arrow-down-up';
    import ListFilterIcon from '@lucide/svelte/icons/list-filter';
    import ChannelAvatar from './ChannelAvatar.svelte';
    import ChannelOptionMenu from './ChannelOptionMenu.svelte';
    import { fileTypeFamily, fileTypeIcon, type FileTypeFamily } from '../file-list/file-type';
    import { activeTab, driveSwitcherOpen } from '../mobile/mobile-shell-store';
    import { pushSheet } from '../modals/sheet-stack';
    import { toAppError } from '../../modules/errors';
    import { formatBytes, formatDuration, splitNameAndExt } from '../../utils';
    import { relativeTimeLabel } from '../file-list/row-meta';
    import {
        canSaveSourceMedia,
        CHANNEL_SORTS,
        createChannelMediaPager,
        isOpenable,
        MAX_CHANNEL_ITEMS,
        MAX_SOURCE_COPY_SELECTION,
        mediaDetails,
        mediaMeta,
        mediaActionLabel,
        mediaTitle,
        restrictionLabel,
        sortPosts,
        sourceHandle,
        type ChannelMediaFetcher,
        type ChannelMediaItem,
        type ChannelMediaKind,
        type ChannelMediaView,
        type ChannelPageMemory,
        type ChannelPostActions,
        type ChannelSort,
        type ChannelSource,
    } from './channel-model';

    interface Props {
        source: ChannelSource;
        fetchMedia: ChannelMediaFetcher;
        fetchTopics?: ChannelTopicFetcher;
        /** `posts` is the list as shown, which a video queues as its playlist. */
        onOpenPost: (item: ChannelMediaItem, source: ChannelSource, posts: readonly ChannelMediaItem[]) => void | Promise<void>;
        onOpenTelegram: (url: string) => void;
        /** The phone top bar's ⋯. Desktop offers the same actions on the sidebar row. */
        onActions?: (x: number, y: number, source: ChannelSource) => void;
        onPostActions: (x: number, y: number, item: ChannelMediaItem, source: ChannelSource, posts: readonly ChannelMediaItem[], actions: ChannelPostActions) => void;
        onSavePosts?: (source: ChannelSource, items: readonly ChannelMediaItem[], suggestedFolder: string) => void;
        /** Phone only: its top bar needs a way back, desktop leaves by the sidebar. */
        onBack?: () => void;
        /** The first page from an earlier visit, shown while the fresh one loads. */
        recentPages?: ChannelPageMemory;
        /** Desktop: the app's top bar, where the channel's title and view
            controls take the folder path's and search box's place. */
        headerHost?: HTMLElement | null;
        loadPhoto?: (source: ChannelSource) => Promise<string>;
        mobile?: boolean;
    }

    let { source, fetchMedia, fetchTopics, onOpenPost, onOpenTelegram, onActions, onPostActions, onSavePosts, onBack, recentPages, headerHost, loadPhoto, mobile = false }: Props = $props();

    const KINDS: ReadonlyArray<{ value: ChannelMediaKind; label: string }> = [
        { value: 'all', label: 'All' },
        { value: 'video', label: 'Videos' },
        { value: 'audio', label: 'Audio' },
        { value: 'image', label: 'Images' },
        { value: 'document', label: 'Documents' },
    ];
    // The desktop menu names its choice on the trigger, where a bare "All"
    // would not say all of what.
    const KIND_MENU = KINDS.map((option) => option.value === 'all' ? { ...option, label: 'All files' } : option);
    const SEARCH_DELAY_MS = 300;
    const SKELETON_WIDTHS = [62, 44, 71, 38, 56, 49];

    const pager = createChannelMediaPager((request) => fetchMedia(request));

    let selectedTopic = $state<ChannelTopic | undefined>(untrack(() => recentPages?.selectedTopic?.(source)));
    let forum = $state(untrack(() => Boolean(source.forum || selectedTopic)));
    let topicNames = $state<ReadonlyMap<number, ChannelTopic>>(new Map());
    let view = $state<ChannelMediaView>({ status: 'idle' });
    let kind = $state<ChannelMediaKind>('all');
    let sort = $state<ChannelSort>('newest');
    let query = $state('');
    let appliedQuery = $state('');
    let scroller = $state<HTMLElement | null>(null);
    let list = $state<HTMLElement | null>(null);
    let sentinel = $state<HTMLElement | null>(null);
    let searchInput = $state<HTMLInputElement | null>(null);
    let searchOpen = $state(false);
    let scrolled = $state(false);
    let selected = $state<ReadonlySet<string>>(new Set());
    let selectionAnchor: string | undefined;
    // Reaching the end loads older posts only while they keep adding rows. In
    // a channel with few videos the end stays in view, so without this it
    // would page through the whole history, request after request.
    let olderAddedRows = $state(true);
    let viewVersion = 0;
    let searchTimer: ReturnType<typeof setTimeout> | undefined;

    const items = $derived('items' in view ? view.items : []);
    // What the list shows, and so what a video queues, in the chosen order.
    const shown = $derived(sortPosts(items, sort));
    const selectedItems = $derived(shown.filter((item) => canSaveSourceMedia(source, item) && selected.has(selectionKey(item))));
    const touchSelecting = $derived(mobile && selectedItems.length > 0);
    const handlesMobileBack = $derived(mobile && $activeTab === 'files' && !$driveSwitcherOpen);
    const suggestedFolder = $derived(selectedTopic?.title ?? source.title);
    const browsingSource = $derived(selectedTopic ? { ...source, title: `${source.title} · ${selectedTopic.title}` } : source);
    const searchLabel = $derived(selectedTopic ? `Search files in ${selectedTopic.title}` : 'Search files');
    const topicUnavailable = $derived(view.status === 'error' && typeof view.error === 'object' && view.error !== null && 'code' in view.error && ['topic_unavailable', 'forum_unavailable'].includes(String(view.error.code)));
    const handle = $derived(sourceHandle(source));
    const failure = $derived(view.status === 'error' ? toAppError(view.error, { source: 'backend' }) : null);
    const empty = $derived.by(() => {
        if (appliedQuery) {
            return {
                title: `No results for “${appliedQuery}”`,
                body: kind === 'all' ? 'Try a different word.' : 'Choose All file types to include every format.',
            };
        }
        const noun = { all: 'files', video: 'videos', audio: 'audio', image: 'images', document: 'documents' }[kind];
        const older = view.status === 'empty' && view.hasMore;
        return {
            title: selectedTopic ? `No ${noun} in ${selectedTopic.title}` : `No ${noun} yet`,
            body: older ? 'None in the latest posts.' : 'Images, documents, video and audio show up here.',
        };
    });

    async function load(append = false): Promise<void> {
        if (pager.snapshot().status === 'loading') return;
        const current = ++viewVersion;
        const shown = items.length;
        const pending = pager.load({ append });
        view = pager.snapshot();
        const fresh = await pending;
        if (current !== viewVersion) return;
        view = fresh;
        syncTopicMetadata();
        olderAddedRows = !append || items.length > shown;
        if (!append) remember();
    }

    // The unfiltered first page is what a return visit shows at once.
    function remember(): void {
        if (view.status === 'ready' && kind === 'all' && !appliedQuery) recentPages?.set(source, view.items, selectedTopic?.id ?? 0);
    }

    function syncTopicMetadata(): void {
        const scope = pager.scope();
        if (view.status === 'ready' || view.status === 'empty') forum = scope.forum;
        rememberTopics(scope.topics);
    }

    function rememberTopics(topics: readonly ChannelTopic[]): void {
        const next = mergeTopicMetadata(topicNames, topics);
        topicNames = next;
        if (selectedTopic && next.has(selectedTopic.id)) {
            selectedTopic = next.get(selectedTopic.id);
            recentPages?.selectTopic?.(source, selectedTopic);
        }
    }

    function chooseTopic(next: ChannelTopic | undefined): void {
        if ((next?.id ?? 0) === (selectedTopic?.id ?? 0)) return;
        selectedTopic = next;
        recentPages?.selectTopic?.(source, next);
        pager.setTopic(next?.id ?? 0);
        reload();
    }

    function topicLabel(item: ChannelMediaItem): string {
        if (!forum || selectedTopic || !item.topicId) return '';
        return topicNames.get(item.topicId)?.title ?? `Topic ${item.topicId}`;
    }

    function reload(): void {
        endSelection();
        pager.setFilter(appliedQuery, kind);
        scroller?.scrollTo({ top: 0 });
        void load();
    }

    function chooseKind(next: ChannelMediaKind): void {
        if (next === kind) return;
        kind = next;
        reload();
    }

    function chooseSort(next: ChannelSort): void {
        sort = next;
        scroller?.scrollTo({ top: 0 });
    }

    function onQueryInput(): void {
        clearTimeout(searchTimer);
        searchTimer = setTimeout(() => {
            if (query.trim() === appliedQuery) return;
            appliedQuery = query.trim();
            reload();
        }, SEARCH_DELAY_MS);
    }

    function clearQuery(): void {
        clearTimeout(searchTimer);
        query = '';
        if (!appliedQuery) return;
        appliedQuery = '';
        reload();
    }

    function onSearchKeydown(event: KeyboardEvent): void {
        if (event.key !== 'Escape' || !query) return;
        event.stopPropagation();
        clearQuery();
    }

    async function toggleSearch(): Promise<void> {
        if (searchOpen) {
            closeSearch();
            return;
        }
        searchOpen = true;
        await tick();
        searchInput?.focus();
    }

    function closeSearch(): void {
        searchOpen = false;
        clearQuery();
    }

    // The menu closes on any document click, so this one must not reach it.
    function openActions(event: MouseEvent): void {
        event.stopPropagation();
        const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
        onActions?.(rect.left, rect.bottom + 4, source);
    }

    // Moves the desktop header into the app's top bar, and takes it out again
    // with the view, wherever it was put.
    function moveInto(node: HTMLElement, target: HTMLElement | null | undefined) {
        target?.append(node);
        return { destroy: () => node.remove() };
    }

    function bindProtectedPost(row: HTMLElement, protectedContent: boolean) {
        let currentProtection = protectedContent;
        const cleanup = bindViewOnlyGuards(row, () => currentProtection, true);
        return { update: (next: boolean) => { currentProtection = next; }, destroy: cleanup };
    }

    function family(item: ChannelMediaItem): FileTypeFamily {
        if (item.kind === 'video' || item.kind === 'audio' || item.kind === 'image') return item.kind;
        if (item.kind === 'pdf' || item.kind === 'text') return 'document';
        return fileTypeFamily(splitNameAndExt(item.name).ext);
    }

    // A whole channel being protected is said once, in the bar; repeating it
    // on every row would only add noise.
    function badge(item: ChannelMediaItem): string {
        return source.protected && (item.protected || item.blockReason === 'protected') ? '' : restrictionLabel(item);
    }

    function rowLabel(item: ChannelMediaItem): string {
        const parts = [mediaTitle(item), mediaMeta(item).replace(/ · /g, ', '), topicLabel(item), badge(item)].filter(Boolean).join(', ');
        if (isOpenable(item)) return `${mediaActionLabel(item)} ${parts}`;
        return item.telegramUrl ? `Open in Telegram: ${parts}` : `Unavailable in TDrive: ${parts}`;
    }

    function selectionKey(item: ChannelMediaItem): string {
        return `${item.msgId}:${item.copyIdentity}`;
    }

    function selectItem(item: ChannelMediaItem, toggle = false): void {
        if (!onSavePosts || !canSaveSourceMedia(source, item)) {
            if (!toggle) endSelection();
            return;
        }
        const key = selectionKey(item);
        selectionAnchor = key;
        if (!toggle) selected = new Set([key]);
        else if (selected.has(key)) selected = new Set([...selected].filter((entry) => entry !== key));
        else if (selectedItems.length < MAX_SOURCE_COPY_SELECTION) selected = new Set([...selectedItems.map(selectionKey), key]);
    }

    function selectRange(item: ChannelMediaItem, from = selectionAnchor): void {
        if (!onSavePosts) return;
        const end = shown.indexOf(item);
        const anchor = shown.findIndex((entry) => selectionKey(entry) === from);
        if (anchor < 0) {
            selectItem(item);
            return;
        }
        selectionAnchor = from;
        const range = shown.slice(Math.min(anchor, end), Math.max(anchor, end) + 1);
        if (end < anchor) range.reverse();
        selected = new Set(range.filter((entry) => canSaveSourceMedia(source, entry)).slice(0, MAX_SOURCE_COPY_SELECTION).map(selectionKey));
    }

    function endSelection(): void {
        selectionAnchor = undefined;
        selected = new Set();
    }

    function focusSelectedRow(): void {
        list?.querySelector<HTMLButtonElement>('.channel-row-main[aria-pressed="true"]')?.focus({ preventScroll: true });
    }

    function cancelSelection(): void {
        focusSelectedRow();
        endSelection();
    }

    function onCopyActionKeydown(event: KeyboardEvent): void {
        if (event.key !== 'Escape') return;
        event.preventDefault();
        event.stopPropagation();
        cancelSelection();
    }

    function saveSelection(): void {
        if (!selectedItems.length) return;
        focusSelectedRow();
        onSavePosts?.(source, selectedItems, suggestedFolder);
        endSelection();
    }

    function openPostActions(event: MouseEvent, item: ChannelMediaItem): void {
        event.preventDefault();
        showRowActions(event.clientX, event.clientY, item);
    }

    function showRowActions(x: number, y: number, item: ChannelMediaItem): void {
        const copyable = canSaveSourceMedia(source, item);
        const select = mobile && onSavePosts && copyable ? () => {
            selectItem(item);
            // The row's action button is removed in selection mode.
            void tick().then(focusSelectedRow);
        } : undefined;
        const selection = copyable && selected.has(selectionKey(item)) && selectedItems.length > 1
            ? { count: selectedItems.length, save: saveSelection }
            : undefined;
        onPostActions(x, y, item, browsingSource, shown, { suggestedFolder, select, selection });
    }

    // The phone row's trailing "…": a visible way into the same actions the long
    // press opens, positioned under the button rather than at a pointer it has.
    function openRowActions(event: MouseEvent, item: ChannelMediaItem): void {
        event.stopPropagation();
        const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
        showRowActions(rect.left, rect.bottom + 4, item);
    }

    function handleRowKeydown(event: KeyboardEvent, item: ChannelMediaItem): void {
        if (event.key !== 'ContextMenu' && !(event.shiftKey && event.key === 'F10')) return;
        event.preventDefault();
        const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
        showRowActions(rect.left, rect.bottom + 4, item);
    }

    function activateRow(event: MouseEvent, item: ChannelMediaItem): void {
        if (!isOpenable(item) && !item.telegramUrl && canSaveSourceMedia(source, item)) {
            openRowActions(event, item);
        } else {
            void onOpenPost(item, browsingSource, shown);
        }
    }

    function clickedChip(event: MouseEvent): boolean {
        return event.target instanceof Element && !!event.target.closest('.channel-chip');
    }

    function handleRowClick(event: MouseEvent, item: ChannelMediaItem): void {
        if (mobile) {
            if (touchSelecting) selectItem(item, true);
            else activateRow(event, item);
            return;
        }
        if (event.detail === 0) {
            activateRow(event, item);
            return;
        }
        (event.currentTarget as HTMLButtonElement).focus({ preventScroll: true });
        if (event.shiftKey) selectRange(item);
        else if (event.metaKey || event.ctrlKey) selectItem(item, true);
        else if (clickedChip(event)) {
            if (event.detail === 1) activateRow(event, item);
        } else {
            selectItem(item);
        }
    }

    function handleRowDoubleClick(event: MouseEvent, item: ChannelMediaItem): void {
        if (!mobile && !event.shiftKey && !event.metaKey && !event.ctrlKey && !clickedChip(event)) activateRow(event, item);
    }

    // Arrow keys walk the rows the way they walk the file list.
    function onListKeydown(event: KeyboardEvent): void {
        if (event.key === 'Escape' && selectedItems.length) {
            event.preventDefault();
            event.stopPropagation();
            endSelection();
            return;
        }
        if (event.altKey) return;
        const rows = Array.from((event.currentTarget as HTMLElement).querySelectorAll<HTMLButtonElement>('.channel-row-main'));
        const index = rows.indexOf(document.activeElement as HTMLButtonElement);
        if (index < 0) return;
        if (event.key === ' ' && onSavePosts && canSaveSourceMedia(source, shown[index])) {
            event.preventDefault();
            if (event.shiftKey) selectRange(shown[index]);
            else if (!event.repeat) selectItem(shown[index], true);
            return;
        }
        if (event.metaKey || event.ctrlKey) return;
        const next = { ArrowDown: index + 1, ArrowUp: index - 1, Home: 0, End: rows.length - 1 }[event.key];
        if (next === undefined) return;
        event.preventDefault();
        const target = Math.max(0, Math.min(rows.length - 1, next));
        if (event.shiftKey) selectRange(shown[target], selectionAnchor ?? selectionKey(shown[index]));
        rows[target]?.focus();
    }

    $effect(() => {
        if (!mobile || !(source.protected || shown.some((item) => item.protected))) return;
        return acquireScreenProtection(setScreenProtect);
    });

    $effect(() => {
        if (!sentinel || !scroller || typeof IntersectionObserver === 'undefined') return;
        const observer = new IntersectionObserver((entries) => {
            if (entries.some((entry) => entry.isIntersecting)) void load(true);
        }, { root: scroller, rootMargin: '0px 0px 480px 0px' });
        observer.observe(sentinel);
        return () => observer.disconnect();
    });

    // A phone has no hover or right-click: a long press opens the post's menu,
    // the way it does on a file row.
    $effect(() => {
        if (!mobile || !list) return;
        return bindLongPress(list, '.channel-row', (row, x, y) => {
            const item = items.find((entry) => String(entry.msgId) === row.dataset.msgId);
            if (item && !touchSelecting) showRowActions(x, y, item);
        });
    });

    $effect(() => {
        if (!handlesMobileBack || !searchOpen) return;
        const back = pushSheet(closeSearch);
        return () => back.release();
    });

    $effect(() => {
        if (!handlesMobileBack || !touchSelecting) return;
        const back = pushSheet(cancelSelection);
        return () => back.release();
    });

    onMount(() => {
        pager.select(source);
        pager.setTopic(selectedTopic?.id ?? 0);
        const recent = recentPages?.get(source, selectedTopic?.id ?? 0);
        if (recent?.length) {
            // Shown until the fresh page replaces it. No more pages are asked
            // for in the meantime, so nothing appends to rows about to go.
            view = { status: 'ready', items: recent, hasMore: false, capped: false };
            const current = ++viewVersion;
            void pager.load().then((fresh) => {
                if (current !== viewVersion) return;
                view = fresh;
                syncTopicMetadata();
                remember();
            });
        } else {
            void load();
        }
        return () => {
            viewVersion += 1;
            clearTimeout(searchTimer);
            pager.invalidate();
        };
    });
</script>

{#snippet kindPicker()}
    <div class="segmented channel-kinds" role="group" aria-label="Show">
        {#each KINDS as option (option.value)}
            <button type="button" aria-pressed={kind === option.value} onclick={() => chooseKind(option.value)}>{option.label}</button>
        {/each}
    </div>
{/snippet}

{#snippet copyActions()}
    <div class="channel-copy-actions" role="group" aria-label="Save source files">
        <button type="button" class="secondary-btn" onclick={cancelSelection} onkeydown={onCopyActionKeydown}>Cancel selection</button>
        <button type="button" class="primary-btn" onclick={saveSelection} onkeydown={onCopyActionKeydown}>
            {selectedItems.length === 1 ? 'Save 1 file to My Drive' : `Save ${selectedItems.length} files to My Drive`}
        </button>
    </div>
{/snippet}

<!-- Desktop: the same floating pill Files shows over its list, so a
     selection reads the same wherever it is made. -->
{#snippet selectionBar()}
    <div class="selection-bar channel-selection-bar" role="group" aria-label="Save source files">
        <span class="selection-count">{selectedItems.length === 1 ? '1 file selected' : `${selectedItems.length} files selected`}</span>
        <button type="button" class="primary-btn channel-selection-save" onclick={saveSelection} onkeydown={onCopyActionKeydown}>
            {selectedItems.length === 1 ? 'Save 1 file to My Drive' : `Save ${selectedItems.length} files to My Drive`}
        </button>
        <button type="button" class="selection-btn ghost" title="Cancel selection" aria-label="Cancel selection" onclick={cancelSelection} onkeydown={onCopyActionKeydown}>
            <XIcon size={16} strokeWidth={2} aria-hidden="true" />
        </button>
    </div>
{/snippet}

{#snippet chip(item: ChannelMediaItem, TypeIcon: ReturnType<typeof fileTypeIcon>, openable: boolean)}
    <span class="file-type-icon channel-chip" aria-hidden="true">
        <span class="chip-layer chip-type"><TypeIcon size={20} strokeWidth={1.5} /></span>
        <span class="chip-layer chip-action">
            {#if openable && mediaActionLabel(item) === 'Play'}<PlayIcon size={16} strokeWidth={2} fill="currentColor" />{:else if openable}<EyeIcon size={16} strokeWidth={2} />{:else}<ExternalLinkIcon size={16} strokeWidth={2} />{/if}
        </span>
    </span>
{/snippet}

{#snippet row(item: ChannelMediaItem)}
    {@const openable = isOpenable(item)}
    {@const copyable = canSaveSourceMedia(source, item)}
    {@const isSelected = copyable && selected.has(selectionKey(item))}
    {@const TypeIcon = fileTypeIcon(family(item))}
    {@const label = badge(item)}
    <li class="channel-row" class:is-selected={isSelected} class:is-locked={!openable && !copyable} class:is-protected={source.protected || item.protected} use:bindProtectedPost={Boolean(source.protected || item.protected)} data-msg-id={item.msgId}>
        <button
            class="channel-row-main"
            type="button"
            title={mediaDetails(item) || undefined}
            aria-label={rowLabel(item)}
            aria-pressed={onSavePosts && copyable && (!mobile || touchSelecting) ? isSelected : undefined}
            onclick={(event) => handleRowClick(event, item)}
            ondblclick={(event) => handleRowDoubleClick(event, item)}
            oncontextmenu={mobile ? undefined : (event) => openPostActions(event, item)}
            onkeydown={(event) => handleRowKeydown(event, item)}
        >
            {#if mobile}
                {@render chip(item, TypeIcon, openable)}
                <span class="channel-row-text">
                    <span class="channel-row-title">{mediaTitle(item)}</span>
                    <span class="channel-row-meta">{mediaMeta(item)}{#if topicLabel(item)}<span class="channel-row-topic"> · {topicLabel(item)}</span>{/if}</span>
                </span>
                {#if label}<span class="channel-row-badge" title={label === 'Protected' ? 'View only. Saving and forwarding are disabled.' : undefined}>{label}</span>{/if}
            {:else}
                <!-- Name, date and size in the file table's columns. The
                     kind is the icon's to say; a length is the one figure
                     a list of videos is scanned for, so it trails the name. -->
                <span class="channel-row-name">
                    {@render chip(item, TypeIcon, openable)}
                    <span class="channel-row-title">{mediaTitle(item)}</span>
                    {#if item.duration > 0}<span class="channel-row-length">{formatDuration(item.duration)}</span>{/if}
                    {#if topicLabel(item)}<span class="channel-row-topic">{topicLabel(item)}</span>{/if}
                    {#if label === 'Protected'}
                        <span class="channel-row-lock" title="View only. Saving and forwarding are disabled."><LockIcon size={13} strokeWidth={2.25} aria-hidden="true" /></span>
                    {:else if label}
                        <span class="channel-row-badge">{label}</span>
                    {/if}
                </span>
                <span class="channel-row-date">{relativeTimeLabel(item.date)}</span>
                <span class="channel-row-size">{item.size > 0 ? formatBytes(item.size) : '—'}</span>
            {/if}
        </button>
        {#if openable && item.telegramUrl && !mobile}
            <button
                class="channel-row-telegram"
                type="button"
                title="Open in Telegram"
                aria-label={`Open ${mediaTitle(item)} in Telegram`}
                onclick={() => onOpenTelegram(item.telegramUrl)}
            >
                <ExternalLinkIcon size={16} strokeWidth={2} aria-hidden="true" />
            </button>
        {/if}
        {#if mobile && !touchSelecting && (openable || item.telegramUrl || copyable)}
            <button
                class="channel-row-actions"
                type="button"
                aria-haspopup="menu"
                aria-label={`Actions for ${mediaTitle(item)}`}
                onclick={(event) => openRowActions(event, item)}
            >
                <EllipsisIcon size={20} strokeWidth={2} aria-hidden="true" />
            </button>
        {/if}
    </li>
{/snippet}

<section class="channel-view" class:is-mobile={mobile} aria-labelledby="channel-view-title">
    {#if mobile}
        <header class="channel-topbar" class:is-scrolled={scrolled}>
            <div class="topbar-row">
                <button type="button" class="topbar-back" aria-label="Back" onclick={touchSelecting ? cancelSelection : onBack}>
                    <ChevronLeftIcon size={24} strokeWidth={2} aria-hidden="true" />
                </button>
                <div class="topbar-folder-titles">
                    <h1 id="channel-view-title" class="topbar-folder-title">{source.title}</h1>
                    <span class="topbar-folder-meta">{handle}{source.protected ? ' · Protected' : ''}</span>
                </div>
                <div class="topbar-actions">
                    <button
                        type="button"
                        class="topbar-icon-btn"
                        aria-expanded={searchOpen}
                        aria-label={searchOpen ? 'Hide search' : 'Search posts'}
                        onclick={toggleSearch}
                    >
                        <SearchIcon size={22} strokeWidth={2} aria-hidden="true" />
                    </button>
                    <ChannelOptionMenu iconOnly name="Sort by" options={CHANNEL_SORTS} value={sort} onChange={chooseSort} icon={ArrowDownUpIcon} />
                    <button type="button" class="topbar-icon-btn" aria-haspopup="menu" aria-label="Source actions" onclick={openActions}>
                        <EllipsisIcon size={22} strokeWidth={2} aria-hidden="true" />
                    </button>
                </div>
            </div>
            <div class="topbar-search-shell" data-open={searchOpen} inert={!searchOpen}>
                <div class="topbar-search">
                    <SearchIcon class="topbar-search-icon" size={18} strokeWidth={2} aria-hidden="true" />
                    <input
                        bind:this={searchInput}
                        bind:value={query}
                        oninput={onQueryInput}
                        type="text"
                        enterkeyhint="search"
                        maxlength="120"
                        placeholder={searchLabel}
                        autocomplete="off"
                        spellcheck="false"
                        aria-label={selectedTopic ? searchLabel : `Search files in ${source.title}`}
                    />
                </div>
            </div>
            {#if forum && fetchTopics}<ChannelTopicPicker {source} {mobile} fetchTopics={fetchTopics} selected={selectedTopic} onChoose={chooseTopic} onTopics={rememberTopics} />{/if}
            <div class="channel-topbar-kinds">{@render kindPicker()}</div>
        </header>
    {:else}
        <div class="channel-header" use:moveInto={headerHost}>
            <div class="channel-heading">
                {#if loadPhoto}<span class="channel-heading-avatar"><ChannelAvatar {source} {loadPhoto} /></span>{/if}
                <h1 id="channel-view-title" class="channel-heading-title" title={source.title}>{source.title}</h1>
                {#if source.protected}
                    <span class="channel-heading-protected" title="View only. Saving and forwarding are disabled.">
                        <LockIcon size={12} strokeWidth={2.25} aria-hidden="true" />
                        Protected
                    </span>
                {/if}
                <span class="channel-heading-handle">{handle}</span>
            </div>
            <div class="channel-tools">
                {#if forum && fetchTopics}<ChannelTopicPicker {source} fetchTopics={fetchTopics} selected={selectedTopic} onChoose={chooseTopic} onTopics={rememberTopics} />{/if}
                <ChannelOptionMenu name="File type" options={KIND_MENU} value={kind} onChange={chooseKind} icon={ListFilterIcon} />
                <ChannelOptionMenu name="Sort by" options={CHANNEL_SORTS} value={sort} onChange={chooseSort} icon={ArrowDownUpIcon} />
                <label class="search-field channel-search">
                    <SearchIcon size={14} strokeWidth={2} aria-hidden="true" />
                    <input
                        bind:value={query}
                        oninput={onQueryInput}
                        onkeydown={onSearchKeydown}
                        type="text"
                        maxlength="120"
                        placeholder={selectedTopic ? searchLabel : `Search ${source.title}`}
                        autocomplete="off"
                        spellcheck="false"
                        aria-label={selectedTopic ? searchLabel : `Search files in ${source.title}`}
                    />
                    {#if query}
                        <button class="channel-search-clear" type="button" aria-label="Clear search" onclick={clearQuery}>
                            <XIcon size={12} strokeWidth={2.5} aria-hidden="true" />
                        </button>
                    {/if}
                </label>
            </div>
        </div>
        <div class="channel-columns" aria-hidden="true">
            <span>Name</span>
            <span>Date</span>
            <span class="channel-columns-size">Size</span>
            <span></span>
        </div>
    {/if}

    <div bind:this={scroller} class="channel-scroll" onscroll={() => { scrolled = (scroller?.scrollTop ?? 0) > 2; }}>
        {#if (view.status === 'idle' || view.status === 'loading') && items.length === 0}
            <div class="channel-skeleton" role="status" aria-busy="true" aria-label="Loading posts">
                {#each SKELETON_WIDTHS as width, index (index)}
                    <div class="channel-skeleton-row" style:--skeleton-delay={`${index * 65}ms`}>
                        <span class="file-state-skeleton-icon"></span>
                        <span class="channel-skeleton-text">
                            <span class="file-state-skeleton-bar" style:--skeleton-width={`${width}%`}></span>
                            <span class="file-state-skeleton-bar" style:--skeleton-width="22%"></span>
                        </span>
                    </div>
                {/each}
            </div>
        {:else if failure && items.length === 0}
            <div class="file-state is-error" role="alert">
                <div class="file-state-icon" aria-hidden="true"><TriangleAlertIcon size={24} strokeWidth={2} /></div>
                <div class="file-state-title">{topicUnavailable ? 'Topic unavailable' : 'Posts did not load'}</div>
                <div class="file-state-body">{failure.message}</div>
                <div class="file-state-actions">
                    <button class="secondary-btn" type="button" onclick={() => void load()}>Try again</button>
                    {#if selectedTopic}<button class="secondary-btn" type="button" onclick={() => chooseTopic(undefined)}>View all topics</button>{/if}
                </div>
            </div>
        {:else if view.status === 'empty'}
            <div class="file-state is-empty" role="status">
                <div class="file-state-icon" aria-hidden="true"><RadioTowerIcon size={24} strokeWidth={2} /></div>
                <div class="file-state-title">{empty.title}</div>
                <div class="file-state-body">{empty.body}</div>
                {#if view.hasMore}
                    <div class="file-state-actions">
                        <button class="secondary-btn" type="button" onclick={() => void load(true)}>Look further back</button>
                    </div>
                {/if}
            </div>
        {:else}
            <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
            <ul bind:this={list} class="channel-rows" aria-label={`Posts in ${source.title}`} onkeydown={onListKeydown}>
                {#each shown as item (item.msgId)}
                    {@render row(item)}
                {/each}
            </ul>
            {#if view.status === 'loading'}
                <div class="channel-list-note" role="status"><span class="channel-spinner" aria-hidden="true"></span>Loading older posts</div>
            {:else if failure}
                <div class="channel-list-note" role="alert">
                    {topicUnavailable ? failure.message : 'Older posts did not load.'}
                    <button class="link-button" type="button" onclick={() => void load(true)}>Try again</button>
                    {#if topicUnavailable && selectedTopic}<button class="link-button" type="button" onclick={() => chooseTopic(undefined)}>View all topics</button>{/if}
                </div>
            {:else if view.status === 'ready' && view.capped}
                <div class="channel-list-note">Showing the latest {MAX_CHANNEL_ITEMS} posts. Search to find older ones.</div>
            {:else if view.status === 'ready' && view.hasMore && sort !== 'newest'}
                <!-- Older posts would land anywhere in a sorted list, so they
                     arrive when asked for rather than under the reader. -->
                <div class="channel-list-note">
                    Sorted among the posts loaded so far.
                    <button class="link-button" type="button" onclick={() => void load(true)}>Load older posts</button>
                </div>
            {:else if view.status === 'ready' && view.hasMore && !olderAddedRows}
                <div class="channel-list-note">
                    Nothing more in the posts checked.
                    <button class="link-button" type="button" onclick={() => void load(true)}>Look further back</button>
                </div>
            {:else if view.status === 'ready' && view.hasMore}
                <div bind:this={sentinel} class="channel-sentinel" aria-hidden="true"></div>
            {/if}
        {/if}
    </div>

    {#if touchSelecting}{@render copyActions()}{/if}
    {#if !mobile && selectedItems.length}{@render selectionBar()}{/if}
</section>

<style>
    .channel-copy-actions {
        display: flex;
        justify-content: flex-end;
        align-items: center;
        gap: var(--space-2);
    }
    .channel-copy-actions button { min-height: var(--control-md); }
    .is-mobile .channel-copy-actions {
        flex: 0 0 auto;
        flex-wrap: wrap;
        padding: var(--space-2) calc(var(--space-4) + var(--inset-right)) var(--space-2) calc(var(--space-4) + var(--inset-left));
        border-top: 1px solid var(--border);
    }
    .is-mobile .channel-copy-actions button { min-height: var(--touch-target); }
    .channel-view {
        position: relative;
        flex: 1 1 auto;
        min-width: 0;
        min-height: 0;
        display: flex;
        flex-direction: column;
    }

    /* Desktop: the header sits in the app's top bar, in the folder path's
       place, with the view controls where the search box was. */
    .channel-header {
        flex: 1 1 auto;
        min-width: 0;
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: var(--space-3);
    }

    /* Only seen when no top bar was handed over. */
    .channel-view > .channel-header {
        flex: 0 0 auto;
        min-height: 48px;
        padding: 0 var(--gutter);
        border-bottom: 1px solid var(--color-border-soft);
    }

    .channel-heading {
        flex: 1 1 auto;
        min-width: 0;
        display: flex;
        align-items: center;
        gap: var(--space-2);
    }

    .channel-heading-avatar {
        --avatar-size: 22px;
        flex: 0 0 auto;
        display: inline-flex;
    }

    .channel-heading-title {
        flex: 0 1 auto;
        min-width: 0;
        margin: 0;
        overflow: hidden;
        color: var(--color-text);
        font-size: var(--type-base);
        font-weight: var(--weight-semibold);
        text-overflow: ellipsis;
        white-space: nowrap;
    }

    .channel-heading-protected {
        flex: 0 0 auto;
        display: inline-flex;
        align-items: center;
        gap: 4px;
        color: var(--color-text-muted);
        font-size: var(--type-xs);
        font-weight: var(--weight-medium);
        cursor: default;
    }

    /* Gives way long before the name does, and goes entirely on a narrow
       window rather than dwindling to a stray letter. */
    .channel-heading-handle {
        flex: 0 1000 auto;
        min-width: 0;
        overflow: hidden;
        color: var(--color-text-subtle);
        font-size: var(--type-sm);
        text-overflow: ellipsis;
        white-space: nowrap;
    }

    @media (max-width: 1100px) {
        .channel-heading-handle { display: none; }
    }

    .channel-tools {
        flex: 0 1 auto;
        min-width: 0;
        display: flex;
        align-items: center;
        gap: var(--space-2);
    }

    .channel-search {
        width: clamp(160px, 18vw, 240px);
        height: var(--toolbar-control);
        background: var(--color-surface-1);
        border-color: var(--color-border-soft);
    }

    .channel-search :global(svg) { flex: 0 0 auto; color: var(--color-text-subtle); }

    .channel-search-clear {
        flex: 0 0 auto;
        display: grid;
        width: 18px;
        height: 18px;
        place-items: center;
        border: 0;
        border-radius: var(--radius-pill);
        background: var(--overlay-neutral-3);
        color: var(--color-text);
        cursor: pointer;
    }

    .channel-search-clear:hover { background: var(--color-surface-3); }
    .channel-search-clear:focus-visible { outline: none; box-shadow: var(--focus-ring); }

    /* The file table's column labels, at its measurements, so a channel reads
       as a sibling of a folder. The rows below line up under them. */
    .channel-columns {
        flex: 0 0 auto;
        display: grid;
        grid-template-columns: var(--file-columns);
        align-items: center;
        min-height: 32px;
        padding: 0 var(--gutter);
        column-gap: var(--space-3);
        border-bottom: 1px solid var(--color-border-soft);
        color: var(--color-text-subtle);
        font-size: var(--type-xs);
        font-weight: var(--weight-medium);
    }

    .channel-columns-size { text-align: right; }

    /* The shared .selection-bar draws the pill. */
    .channel-selection-bar { gap: var(--space-2); }
    .channel-selection-bar .selection-count { margin-right: var(--space-1); }
    .channel-selection-save {
        width: auto;
        min-height: 30px;
        padding: 0 var(--space-3);
        border-radius: var(--radius-pill);
        font-size: var(--type-sm);
    }

    .channel-scroll {
        flex: 1 1 auto;
        min-height: 0;
        overflow-y: auto;
        /* The bottom clears the floating selection pill. */
        padding: 4px 4px 72px;
    }

    .channel-rows {
        display: flex;
        flex-direction: column;
        list-style: none;
    }

    .channel-row {
        position: relative;
        display: flex;
        align-items: center;
        border-radius: var(--radius-sm);
        transition: background-color var(--motion-fast) var(--ease-standard);
    }

    .channel-row.is-protected {
        user-select: none;
        -webkit-user-select: none;
        -webkit-touch-callout: none;
    }

    @media (hover: hover) {
        .channel-row:hover { background: var(--overlay-neutral-1); }
    }

    .channel-row.is-selected { background: var(--overlay-accent-1); }

    @media (hover: hover) {
        .channel-row.is-selected:hover { background: var(--overlay-accent-2); }
    }

    /* One row of the file table: the name cell holds the icon, the title and
       whatever qualifies it, then the date and size columns. The last column
       is left for the Telegram button laid over it. */
    .channel-row-main {
        flex: 1 1 auto;
        min-width: 0;
        display: grid;
        grid-template-columns: var(--file-columns);
        align-items: center;
        min-height: 40px;
        padding: 0 12px;
        column-gap: var(--space-3);
        border: 0;
        border-radius: inherit;
        background: transparent;
        color: inherit;
        font: inherit;
        text-align: left;
        cursor: pointer;
        user-select: none;
        -webkit-user-select: none;
    }

    .channel-row-main:focus-visible {
        outline: none;
        box-shadow: inset var(--focus-ring);
    }

    /* The leading icon still opens the file with a single click; on hover it
       turns into the action that click takes. */
    .channel-row-name {
        min-width: 0;
        display: flex;
        align-items: center;
        gap: var(--space-2);
    }

    .channel-chip { position: relative; margin-right: 2px; }

    .chip-layer {
        position: absolute;
        inset: 0;
        display: grid;
        place-items: center;
        transition:
            opacity var(--motion-fast) var(--ease-standard),
            transform var(--motion-fast) var(--ease-standard);
    }

    .chip-action {
        opacity: 0;
        transform: scale(0.8);
        color: var(--color-text);
    }

    .chip-type :global(svg) { width: 18px; height: 18px; }
    .chip-action :global(svg) { width: 14px; height: 14px; }

    @media (hover: hover) {
        .channel-view:not(.is-mobile) .channel-row:not(.is-locked):hover .chip-type,
        .channel-row:not(.is-locked) .channel-row-main:focus-visible .chip-type {
            opacity: 0;
            transform: scale(0.8);
        }

        .channel-view:not(.is-mobile) .channel-row:not(.is-locked):hover .chip-action,
        .channel-row:not(.is-locked) .channel-row-main:focus-visible .chip-action {
            opacity: 1;
            transform: none;
        }
    }

    .channel-row-title {
        flex: 0 1 auto;
        min-width: 0;
        overflow: hidden;
        color: var(--color-text);
        font-size: var(--type-base);
        font-weight: var(--weight-medium);
        text-overflow: ellipsis;
        white-space: nowrap;
    }

    .channel-row-length,
    .channel-row-topic {
        flex: 0 0 auto;
        color: var(--color-text-muted);
        font-size: var(--type-sm);
        font-variant-numeric: tabular-nums;
        white-space: nowrap;
    }

    .channel-row-lock,
    .channel-row-badge { flex: 0 0 auto; }

    .channel-row-lock {
        display: inline-flex;
        color: var(--color-text-muted);
    }

    .channel-row-badge {
        height: 18px;
        display: inline-grid;
        place-items: center;
        padding: 0 6px;
        border-radius: var(--radius-xs);
        background: var(--overlay-neutral-2);
        color: var(--color-text-muted);
        font-size: var(--type-xs);
        font-weight: var(--weight-medium);
    }

    .channel-row-date,
    .channel-row-size {
        min-width: 0;
        overflow: hidden;
        color: var(--color-text-muted);
        font-size: var(--type-sm);
        font-variant-numeric: tabular-nums;
        text-overflow: ellipsis;
        white-space: nowrap;
    }

    .channel-row-size { text-align: right; }

    .channel-row-telegram {
        position: absolute;
        right: 12px;
        display: grid;
        width: 28px;
        height: 28px;
        place-items: center;
        border: 0;
        border-radius: var(--radius-sm);
        background: transparent;
        color: var(--color-text-muted);
        cursor: pointer;
        transition:
            opacity var(--motion-fast) var(--ease-standard),
            background-color var(--motion-fast) var(--ease-standard),
            color var(--motion-fast) var(--ease-standard);
    }

    @media (hover: hover) {
        .channel-row-telegram:hover { background: var(--overlay-neutral-2); color: var(--color-text); }
    }

    .channel-row-telegram:focus-visible { outline: none; box-shadow: var(--focus-ring); }

    @media (hover: hover) {
        .channel-row-telegram { opacity: 0; }

        .channel-row:hover .channel-row-telegram,
        .channel-row:focus-within .channel-row-telegram { opacity: 1; }
    }

    /* Placeholder rows hold the table's row height and icon column. */
    .channel-skeleton {
        display: flex;
        flex-direction: column;
    }

    .channel-skeleton-row {
        display: flex;
        align-items: center;
        gap: 10px;
        min-height: 40px;
        padding: 0 12px;
    }

    .channel-skeleton-row .file-state-skeleton-icon {
        flex: 0 0 auto;
        width: 20px;
        height: 20px;
        border-radius: var(--radius-xs);
    }

    .channel-skeleton-text {
        flex: 1 1 auto;
        min-width: 0;
        display: flex;
        flex-direction: column;
    }

    .channel-skeleton-text .file-state-skeleton-bar + .file-state-skeleton-bar { display: none; }

    .channel-list-note {
        display: flex;
        align-items: center;
        justify-content: center;
        gap: var(--space-2);
        padding: var(--space-5) var(--space-4);
        color: var(--text-muted);
        font-size: var(--type-sm);
        text-align: center;
    }

    .channel-spinner {
        width: 14px;
        height: 14px;
        border: 2px solid var(--border);
        border-top-color: var(--accent);
        border-radius: var(--radius-pill);
        animation: channel-spin 720ms linear infinite;
    }

    .channel-sentinel { height: 1px; }

    @keyframes channel-spin {
        to { transform: rotate(360deg); }
    }

    /* ---- Phone. The shell's top bar steps aside for this one, which restates
       it: same fill to the glass, same insets, a hairline only once rows pass
       under it. The rows become the inset card the file list draws. ---- */
    .channel-topbar {
        flex: 0 0 auto;
        padding: var(--inset-top) var(--inset-right) 0 var(--inset-left);
        border-bottom: 1px solid transparent;
        background: var(--color-surface-0);
        transition: border-color var(--motion-med) var(--ease-standard);
    }

    .channel-topbar.is-scrolled { border-bottom-color: var(--color-border-soft); }
    .channel-topbar .topbar-folder-meta { display: block; }

    /* More file types fit without squeezing labels or shrinking touch targets. */
    .channel-topbar-kinds { overflow-x: auto; padding: 0 var(--space-4) var(--space-3); }
    .channel-topbar-kinds .channel-kinds { display: flex; }
    .channel-topbar-kinds .channel-kinds button {
        flex: 1 0 auto;
        min-height: var(--touch-target);
        font-size: var(--mobile-type-meta);
    }

    .is-mobile .channel-scroll {
        /* The search field opens into this list, so its bottom clears the
           keyboard as well as the tab bar; otherwise the last posts sit under
           the keys while the user is typing to filter them. */
        padding: var(--space-2) 0 calc(var(--space-6) + var(--inset-keyboard));
        overscroll-behavior-y: contain;
        -webkit-overflow-scrolling: touch;
    }

    /* A full-bleed list on the canvas, like the phone's file list: rows are
       parted by hairlines from the text column, not gathered into a card.
       Landscape still keeps them off the notch or the navigation bar. */
    .is-mobile .channel-rows {
        gap: 0;
        margin: 0;
        padding: 0 var(--inset-right) 0 var(--inset-left);
    }

    .is-mobile .channel-row {
        border-radius: 0;
        background: var(--color-canvas);
        transition: background-color var(--motion-fast) var(--ease-standard);
    }

    .is-mobile .channel-row:active { background: var(--color-surface-1); }
    .is-mobile .channel-row.is-selected { background: color-mix(in srgb, var(--color-accent) 14%, var(--color-canvas)); }

    @media (hover: hover) {
        .is-mobile .channel-row:hover:not(.is-selected) { background: var(--color-surface-0); }
    }

    .is-mobile .channel-row + .channel-row::before {
        position: absolute;
        top: 0;
        /* Starts at the text column: 16px padding + 28px icon + 12px gap. */
        left: calc(56px + var(--inset-left));
        right: 0;
        height: 1px;
        background: var(--color-border-soft);
        content: '';
    }

    .is-mobile .channel-row-main {
        display: flex;
        gap: 12px;
        min-height: 60px;
        padding: 8px 2px 8px 16px;
        -webkit-tap-highlight-color: transparent;
        -webkit-touch-callout: none;
        touch-action: manipulation;
        user-select: none;
    }

    .is-mobile .channel-row-main:focus-visible { box-shadow: inset var(--focus-ring); }

    .is-mobile .channel-chip {
        width: 28px;
        height: 28px;
        margin: 0;
        border-radius: var(--radius-sm);
        color: var(--color-text-muted);
    }

    .is-mobile .chip-type :global(svg) { width: 22px; height: 22px; }

    .is-mobile .channel-row-text {
        flex: 1 1 auto;
        min-width: 0;
        display: flex;
        flex-direction: column;
        gap: 2px;
    }

    .is-mobile .channel-row-meta {
        overflow: hidden;
        color: var(--color-text-muted);
        font-variant-numeric: tabular-nums;
        text-overflow: ellipsis;
        white-space: nowrap;
    }

    .is-mobile .channel-row-badge { margin-right: var(--space-1); }

    .is-mobile .channel-row-title { font-size: var(--mobile-type-body); }
    .is-mobile .channel-row-meta { font-size: var(--mobile-type-meta); }

    /* The row drops its own right padding so the "…" lands where the padding
       was, keeping a 44px target without widening the row. */
    .is-mobile .channel-row-main { padding-right: 2px; }
    .is-mobile .channel-row-actions {
        flex: 0 0 auto;
        display: grid;
        place-items: center;
        width: 44px;
        height: 44px;
        margin-right: var(--space-1);
        border: 0;
        border-radius: var(--radius-md);
        background: transparent;
        color: var(--color-text-muted);
        cursor: pointer;
        -webkit-tap-highlight-color: transparent;
        touch-action: manipulation;
    }
    .is-mobile .channel-row-actions:active { background: var(--color-surface-2); color: var(--color-text); }
    .is-mobile .channel-row-actions:focus-visible { outline: none; box-shadow: var(--focus-ring); }

    .is-mobile .channel-skeleton { padding: 0 var(--inset-right) 0 var(--inset-left); }
    .is-mobile .channel-skeleton-row { gap: 12px; min-height: 60px; padding: 8px 16px; }
    .is-mobile .channel-skeleton-row .file-state-skeleton-icon { width: 28px; height: 28px; border-radius: var(--radius-sm); }
    .is-mobile .channel-skeleton-text { gap: 8px; }
    .is-mobile .channel-skeleton-text .file-state-skeleton-bar + .file-state-skeleton-bar { display: block; }
    .is-mobile .channel-list-note { font-size: var(--mobile-type-meta); flex-wrap: wrap; }
    /* Retry / Try again / Load older posts are the one way on from a stuck list,
       so on a phone each is a 44px target instead of a zero-padding text link. */
    .is-mobile .channel-list-note .link-button {
        display: inline-flex;
        align-items: center;
        min-height: var(--touch-target);
        padding: 0 var(--space-2);
    }

    @media (prefers-reduced-motion: reduce) {
        .channel-topbar,
        .channel-row,
        .channel-chip,
        .chip-layer,
        .channel-row-telegram { transition: none; }

        .channel-spinner { animation: none; }
    }
</style>
