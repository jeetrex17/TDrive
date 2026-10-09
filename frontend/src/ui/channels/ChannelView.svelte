<script lang="ts">
    import { onMount, tick } from 'svelte';
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
    import ChannelSortMenu from './ChannelSortMenu.svelte';
    import { fileTypeFamily, fileTypeIcon, type FileTypeFamily } from '../file-list/file-type';
    import { pushSheet } from '../modals/sheet-stack';
    import { toAppError } from '../../modules/errors';
    import { splitNameAndExt } from '../../utils';
    import {
        createChannelMediaPager,
        isOpenable,
        MAX_CHANNEL_ITEMS,
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
        type ChannelSort,
        type ChannelSource,
    } from './channel-model';

    interface Props {
        source: ChannelSource;
        fetchMedia: ChannelMediaFetcher;
        /** `posts` is the list as shown, which a video queues as its playlist. */
        onOpenPost: (item: ChannelMediaItem, source: ChannelSource, posts: readonly ChannelMediaItem[]) => void | Promise<void>;
        onOpenTelegram: (url: string) => void;
        /** The phone top bar's ⋯. Desktop offers the same actions on the sidebar row. */
        onActions?: (x: number, y: number, source: ChannelSource) => void;
        onPostActions: (x: number, y: number, item: ChannelMediaItem, source: ChannelSource, posts: readonly ChannelMediaItem[]) => void;
        /** Phone only: its top bar needs a way back, desktop leaves by the sidebar. */
        onBack?: () => void;
        /** The first page from an earlier visit, shown while the fresh one loads. */
        recentPages?: ChannelPageMemory;
        mobile?: boolean;
    }

    let { source, fetchMedia, onOpenPost, onOpenTelegram, onActions, onPostActions, onBack, recentPages, mobile = false }: Props = $props();

    const KINDS: ReadonlyArray<{ value: ChannelMediaKind; label: string }> = [
        { value: 'all', label: 'All' },
        { value: 'video', label: 'Videos' },
        { value: 'audio', label: 'Audio' },
        { value: 'image', label: 'Images' },
        { value: 'document', label: 'Documents' },
    ];
    const SEARCH_DELAY_MS = 300;
    const SKELETON_WIDTHS = [62, 44, 71, 38, 56, 49];

    const pager = createChannelMediaPager((request) => fetchMedia(request));

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
    // Reaching the end loads older posts only while they keep adding rows. In
    // a channel with few videos the end stays in view, so without this it
    // would page through the whole history, request after request.
    let olderAddedRows = $state(true);
    let searchTimer: ReturnType<typeof setTimeout> | undefined;

    const items = $derived('items' in view ? view.items : []);
    // What the list shows, and so what a video queues, in the chosen order.
    const shown = $derived(sortPosts(items, sort));
    const handle = $derived(sourceHandle(source));
    const failure = $derived(view.status === 'error' ? toAppError(view.error, { source: 'backend' }) : null);
    const empty = $derived.by(() => {
        if (appliedQuery) {
            return {
                title: `No results for “${appliedQuery}”`,
                body: kind === 'all' ? 'Try a different word.' : 'Switch to All to search every post.',
            };
        }
        const noun = { all: 'files', video: 'videos', audio: 'audio', image: 'images', document: 'documents' }[kind];
        const older = view.status === 'empty' && view.hasMore;
        return {
            title: `No ${noun} yet`,
            body: older ? 'None in the latest posts.' : 'Images, documents, video and audio show up here.',
        };
    });

    async function load(append = false): Promise<void> {
        const shown = items.length;
        const pending = pager.load({ append });
        view = pager.snapshot();
        view = await pending;
        olderAddedRows = !append || items.length > shown;
        if (!append) remember();
    }

    // The unfiltered first page is what a return visit shows at once.
    function remember(): void {
        if (view.status === 'ready' && kind === 'all' && !appliedQuery) recentPages?.set(source, view.items);
    }

    function reload(): void {
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
        const parts = [mediaTitle(item), mediaMeta(item).replace(/ · /g, ', '), badge(item)].filter(Boolean).join(', ');
        if (isOpenable(item)) return `${mediaActionLabel(item)} ${parts}`;
        return item.telegramUrl ? `Open in Telegram: ${parts}` : `Unavailable in TDrive: ${parts}`;
    }

    function openPostActions(event: MouseEvent, item: ChannelMediaItem): void {
        event.preventDefault();
        onPostActions(event.clientX, event.clientY, item, source, shown);
    }

    // The phone row's trailing "…": a visible way into the same actions the long
    // press opens, positioned under the button rather than at a pointer it has.
    function openRowActions(event: MouseEvent, item: ChannelMediaItem): void {
        event.stopPropagation();
        const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
        onPostActions(rect.left, rect.bottom + 4, item, source, shown);
    }

    // Arrow keys walk the rows the way they walk the file list.
    function onListKeydown(event: KeyboardEvent): void {
        const rows = Array.from((event.currentTarget as HTMLElement).querySelectorAll<HTMLButtonElement>('.channel-row-main'));
        const index = rows.indexOf(document.activeElement as HTMLButtonElement);
        if (index < 0) return;
        const next = { ArrowDown: index + 1, ArrowUp: index - 1, Home: 0, End: rows.length - 1 }[event.key];
        if (next === undefined) return;
        event.preventDefault();
        rows[Math.max(0, Math.min(rows.length - 1, next))]?.focus();
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
            if (item) onPostActions(x, y, item, source, shown);
        });
    });

    // Android BACK closes the phone's search field before it leaves the channel.
    $effect(() => {
        if (!searchOpen) return;
        const back = pushSheet(closeSearch);
        return () => back.release();
    });

    onMount(() => {
        pager.select(source);
        const recent = recentPages?.get(source);
        if (recent?.length) {
            // Shown until the fresh page replaces it. No more pages are asked
            // for in the meantime, so nothing appends to rows about to go.
            view = { status: 'ready', items: recent, hasMore: false, capped: false };
            void pager.load().then((fresh) => {
                view = fresh;
                remember();
            });
        } else {
            void load();
        }
        return () => {
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

{#snippet row(item: ChannelMediaItem)}
    {@const openable = isOpenable(item)}
    {@const TypeIcon = fileTypeIcon(family(item))}
    {@const label = badge(item)}
    <li class="channel-row" class:is-locked={!openable} class:is-protected={source.protected || item.protected} use:bindProtectedPost={Boolean(source.protected || item.protected)} data-msg-id={item.msgId}>
        <button
            class="channel-row-main"
            type="button"
            title={mediaDetails(item) || undefined}
            aria-label={rowLabel(item)}
            onclick={() => void onOpenPost(item, source, shown)}
            oncontextmenu={mobile ? undefined : (event) => openPostActions(event, item)}
        >
            <span class="file-type-icon channel-chip" aria-hidden="true">
                <span class="chip-layer chip-type"><TypeIcon size={20} strokeWidth={1.5} /></span>
                <span class="chip-layer chip-action">
                    {#if openable && mediaActionLabel(item) === 'Play'}<PlayIcon size={16} strokeWidth={2} fill="currentColor" />{:else if openable}<EyeIcon size={16} strokeWidth={2} />{:else}<ExternalLinkIcon size={16} strokeWidth={2} />{/if}
                </span>
            </span>
            <span class="channel-row-text">
                <span class="channel-row-title">{mediaTitle(item)}</span>
                <span class="channel-row-meta">{mediaMeta(item)}</span>
            </span>
            {#if label}<span class="channel-row-badge" title={label === 'Protected' ? 'View only. Saving and forwarding are disabled.' : undefined}>{label}</span>{/if}
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
        {#if mobile && (openable || item.telegramUrl)}
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
                <button type="button" class="topbar-back" aria-label="Back" onclick={onBack}>
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
                    <ChannelSortMenu iconOnly value={sort} onChange={chooseSort} />
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
                        placeholder="Search posts"
                        autocomplete="off"
                        spellcheck="false"
                        aria-label={`Search posts in ${source.title}`}
                    />
                </div>
            </div>
            <div class="channel-topbar-kinds">{@render kindPicker()}</div>
        </header>
    {:else}
        <header class="channel-bar">
            <div class="channel-bar-heading">
                <h1 id="channel-view-title" class="channel-bar-title" title={source.title}>{source.title}</h1>
                <span class="channel-bar-handle">{handle}</span>
                {#if source.protected}
                    <span class="channel-bar-protected" title="View only. Saving and forwarding are disabled.">
                        <LockIcon size={12} strokeWidth={2.25} aria-hidden="true" />
                        Protected
                    </span>
                {/if}
            </div>
            <div class="channel-bar-tools">
                {@render kindPicker()}
                <ChannelSortMenu value={sort} onChange={chooseSort} />
                <label class="search-field channel-search">
                    <SearchIcon size={15} strokeWidth={2} aria-hidden="true" />
                    <input
                        bind:value={query}
                        oninput={onQueryInput}
                        onkeydown={onSearchKeydown}
                        type="text"
                        maxlength="120"
                        placeholder="Search posts"
                        autocomplete="off"
                        spellcheck="false"
                        aria-label={`Search posts in ${source.title}`}
                    />
                    {#if query}
                        <button class="channel-search-clear" type="button" aria-label="Clear search" onclick={clearQuery}>
                            <XIcon size={13} strokeWidth={2.5} aria-hidden="true" />
                        </button>
                    {/if}
                </label>
            </div>
        </header>
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
                <div class="file-state-title">Posts did not load</div>
                <div class="file-state-body">{failure.message}</div>
                <div class="file-state-actions">
                    <button class="secondary-btn" type="button" onclick={() => void load()}>Try again</button>
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
                    Older posts did not load.
                    <button class="link-button" type="button" onclick={() => void load(true)}>Try again</button>
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
</section>

<style>
    .channel-view {
        flex: 1 1 auto;
        min-width: 0;
        min-height: 0;
        display: flex;
        flex-direction: column;
        background: var(--bg-dark);
    }

    /* The bar takes the breadcrumb row's place and its measurements, so the
       channel reads as a sibling of a folder, Photos and the trash. */
    .channel-bar {
        min-height: 52px;
        flex-shrink: 0;
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: var(--space-4);
        padding: var(--space-2) var(--space-6);
        border-bottom: 1px solid var(--border);
        background: color-mix(in srgb, var(--color-surface-0) 58%, transparent);
    }

    .channel-bar-heading {
        flex: 1 1 auto;
        min-width: 0;
        display: flex;
        align-items: baseline;
        gap: var(--space-2);
    }

    .channel-bar-title {
        flex: 0 1 auto;
        min-width: 0;
        overflow: hidden;
        color: var(--text-main);
        font-size: var(--type-lg);
        font-weight: var(--weight-strong);
        letter-spacing: 0.01em;
        text-overflow: ellipsis;
        white-space: nowrap;
    }

    /* Gives way long before the name does, and goes entirely on a narrow
       window rather than dwindling to a stray letter. */
    .channel-bar-handle {
        flex: 0 1000 auto;
        min-width: 0;
        overflow: hidden;
        color: var(--text-muted);
        font-size: var(--type-sm);
        text-overflow: ellipsis;
        white-space: nowrap;
    }

    @media (max-width: 1100px) {
        .channel-bar-handle { display: none; }
    }

    .channel-bar-protected {
        flex: 0 0 auto;
        display: inline-flex;
        align-self: center;
        align-items: center;
        gap: 4px;
        height: 20px;
        padding: 0 8px;
        border-radius: var(--radius-pill);
        background: var(--overlay-neutral-2);
        color: var(--text-muted);
        font-size: var(--type-xs);
        font-weight: var(--weight-semibold);
        cursor: default;
    }

    .channel-bar-tools {
        flex: 0 0 auto;
        display: flex;
        align-items: center;
        gap: var(--space-2);
    }

    .channel-kinds { flex: 0 0 auto; }

    /* More file types fit without squeezing labels or shrinking touch targets. */
    .channel-topbar-kinds { overflow-x: auto; scrollbar-width: none; }

    .channel-search { width: clamp(150px, 18vw, 220px); }

    .channel-search-clear {
        flex: 0 0 auto;
        display: grid;
        width: 18px;
        height: 18px;
        place-items: center;
        border: 0;
        border-radius: var(--radius-pill);
        background: var(--overlay-neutral-3);
        color: var(--text-main);
        cursor: pointer;
    }

    .channel-search-clear:hover { background: var(--color-surface-3); }
    .channel-search-clear:focus-visible { outline: none; box-shadow: var(--focus-ring); }

    .channel-scroll {
        flex: 1 1 auto;
        min-height: 0;
        overflow-y: auto;
        padding: var(--space-2) 10px var(--space-8);
    }

    .channel-rows {
        display: flex;
        flex-direction: column;
        gap: 2px;
        list-style: none;
    }

    .channel-row {
        position: relative;
        display: flex;
        align-items: center;
        border-radius: var(--radius-md);
        transition: background-color var(--motion-fast) var(--ease-standard);
    }

    .channel-row.is-protected {
        user-select: none;
        -webkit-user-select: none;
        -webkit-touch-callout: none;
    }

    .channel-row:hover,
    .channel-row:focus-within { background: var(--bg-panel); }

    .channel-row-main {
        flex: 1 1 auto;
        min-width: 0;
        display: flex;
        align-items: center;
        gap: 12px;
        padding: 10px 14px;
        border: 0;
        border-radius: inherit;
        background: transparent;
        color: inherit;
        font: inherit;
        text-align: left;
        cursor: pointer;
    }

    .channel-row-main:focus-visible {
        outline: none;
        box-shadow: inset 0 0 0 1px var(--color-accent), var(--focus-ring);
    }

    /* The type chip doubles as the row's action: hovering turns the glyph into
       the thing a click will do, so no row needs a button of its own. */
    .channel-chip {
        position: relative;
        margin-right: 0; /* the row's gap spaces it */
        transition:
            background-color var(--motion-fast) var(--ease-standard),
            border-color var(--motion-fast) var(--ease-standard),
            color var(--motion-fast) var(--ease-standard);
    }

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
    }

    .chip-action :global(svg) {
        width: 16px;
        height: 16px;
    }

    @media (hover: hover) {
        .channel-row:hover .channel-chip,
        .channel-row-main:focus-visible .channel-chip {
            border-color: var(--accent);
            background: var(--accent);
            color: var(--color-on-accent);
        }

        .channel-row.is-locked:hover .channel-chip,
        .channel-row.is-locked .channel-row-main:focus-visible .channel-chip {
            border-color: var(--border);
            background: var(--color-surface-3);
            color: var(--color-text);
        }

        .channel-row:hover .chip-type,
        .channel-row-main:focus-visible .chip-type {
            opacity: 0;
            transform: scale(0.8);
        }

        .channel-row:hover .chip-action,
        .channel-row-main:focus-visible .chip-action {
            opacity: 1;
            transform: none;
        }
    }

    .channel-row-text {
        flex: 1 1 auto;
        min-width: 0;
        display: flex;
        flex-direction: column;
        gap: 2px;
    }

    .channel-row-title,
    .channel-row-meta {
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
    }

    .channel-row-title {
        color: var(--color-text);
        font-size: 0.9rem;
        font-weight: var(--weight-semibold);
        line-height: 1.35;
    }

    .channel-row-meta {
        color: var(--text-muted);
        font-size: 0.8rem;
        font-weight: var(--weight-medium);
        font-variant-numeric: tabular-nums;
        line-height: 1.35;
    }

    .channel-row-badge {
        flex: 0 0 auto;
        height: 20px;
        display: inline-grid;
        place-items: center;
        padding: 0 8px;
        border-radius: var(--radius-pill);
        background: var(--overlay-neutral-2);
        color: var(--text-muted);
        font-size: var(--type-xs);
        font-weight: var(--weight-semibold);
    }

    .channel-row-telegram {
        flex: 0 0 auto;
        display: grid;
        width: 28px;
        height: 28px;
        margin-right: 14px;
        place-items: center;
        border: 0;
        border-radius: var(--radius-sm);
        background: transparent;
        color: var(--text-muted);
        cursor: pointer;
        transition:
            opacity var(--motion-fast) var(--ease-standard),
            background-color var(--motion-fast) var(--ease-standard),
            color var(--motion-fast) var(--ease-standard);
    }

    .channel-row-telegram:hover {
        background: var(--color-surface-3);
        color: var(--color-text);
    }

    .channel-row-telegram:focus-visible { outline: none; box-shadow: var(--focus-ring); }

    @media (hover: hover) {
        .channel-row-telegram { opacity: 0; }

        .channel-row:hover .channel-row-telegram,
        .channel-row:focus-within .channel-row-telegram { opacity: 1; }
    }

    .channel-skeleton {
        display: flex;
        flex-direction: column;
        gap: 2px;
    }

    .channel-skeleton-row {
        display: flex;
        align-items: center;
        gap: 12px;
        min-height: 60px;
        padding: 10px 14px;
    }

    .channel-skeleton-row .file-state-skeleton-icon {
        flex: 0 0 auto;
        width: 40px;
        height: 40px;
        border-radius: var(--radius-sm);
    }

    .channel-skeleton-text {
        flex: 1 1 auto;
        min-width: 0;
        display: flex;
        flex-direction: column;
        gap: 9px;
    }

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

    .channel-topbar-kinds { padding: 0 var(--space-4) var(--space-3); }
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
        scrollbar-width: none;
        overscroll-behavior-y: contain;
        -webkit-overflow-scrolling: touch;
    }

    /* Landscape puts the notch or the navigation bar on a side, so the rows
       stand off it like the rest of the shell rather than running under it. */
    .is-mobile .channel-rows {
        gap: 0;
        margin: 0 calc(var(--space-3) + var(--inset-right)) 0 calc(var(--space-3) + var(--inset-left));
    }

    .is-mobile .channel-row {
        border-radius: 0;
        background: var(--color-surface-0);
        transition: background-color 80ms var(--ease-standard);
    }

    .is-mobile .channel-row:first-child { border-radius: var(--radius-xl) var(--radius-xl) 0 0; }
    .is-mobile .channel-row:last-child { border-bottom-left-radius: var(--radius-xl); border-bottom-right-radius: var(--radius-xl); }
    .is-mobile .channel-row:only-child { border-radius: var(--radius-xl); }
    .is-mobile .channel-row:active { background: var(--color-surface-2); }

    .is-mobile .channel-row + .channel-row::before {
        position: absolute;
        top: 0;
        left: 68px;
        right: 0;
        height: 1px;
        background: var(--color-border-soft);
        content: '';
    }

    .is-mobile .channel-row-main {
        min-height: 68px;
        padding: 10px 14px 10px 12px;
        -webkit-tap-highlight-color: transparent;
        -webkit-touch-callout: none;
        touch-action: manipulation;
        user-select: none;
    }

    .is-mobile .channel-chip {
        width: 44px;
        height: 44px;
        border-color: transparent;
        border-radius: var(--radius-lg);
        background: var(--color-surface-2);
        color: var(--color-text-muted);
    }

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

    .is-mobile .channel-skeleton {
        margin: 0 calc(var(--space-3) + var(--inset-right)) 0 calc(var(--space-3) + var(--inset-left));
    }
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
