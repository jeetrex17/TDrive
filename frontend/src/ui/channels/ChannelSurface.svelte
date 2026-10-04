<script lang="ts">
    import { onMount } from 'svelte';
    import AudioLinesIcon from '@lucide/svelte/icons/audio-lines';
    import ExternalLinkIcon from '@lucide/svelte/icons/external-link';
    import FileIcon from '@lucide/svelte/icons/file';
    import LinkIcon from '@lucide/svelte/icons/link';
    import LockKeyholeIcon from '@lucide/svelte/icons/lock-keyhole';
    import PlayIcon from '@lucide/svelte/icons/play';
    import PlusIcon from '@lucide/svelte/icons/plus';
    import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';
    import SearchIcon from '@lucide/svelte/icons/search';
    import UnplugIcon from '@lucide/svelte/icons/unplug';
    import VideoIcon from '@lucide/svelte/icons/video';
    import XIcon from '@lucide/svelte/icons/x';
    import { formatBytes, formatDate } from '../../utils';
    import Button from '../Button.svelte';
    import IconButton from '../IconButton.svelte';
    import StateView from '../StateView.svelte';
    import { createChannelMediaPager, type ChannelMediaFetcher, type ChannelMediaItem, type ChannelMediaKind, type ChannelMediaView, type ChannelSource } from './channel-model';

    interface Props {
        loadConnected: () => Promise<ChannelSource[]>;
        loadCandidates: () => Promise<ChannelSource[]>;
        connect: (source: ChannelSource) => Promise<ChannelSource>;
        disconnect: (source: ChannelSource) => Promise<void>;
        fetchMedia: ChannelMediaFetcher;
        play: (item: ChannelMediaItem, source: ChannelSource) => Promise<void> | void;
        openTelegram: (url: string) => Promise<void> | void;
        onClose?: () => void;
    }

    let { loadConnected, loadCandidates, connect, disconnect, fetchMedia, play, openTelegram, onClose }: Props = $props();
    const pager = createChannelMediaPager((request) => fetchMedia(request));
    let sources = $state<ChannelSource[]>([]);
    let candidates = $state<ChannelSource[]>([]);
    let selected = $state<ChannelSource | null>(null);
    let media = $state<ChannelMediaView>({ kind: 'idle' });
    let loadingSources = $state(true);
    let sourceError = $state('');
    let search = $state('');
    let kind = $state<ChannelMediaKind>('all');
    let connecting = $state<number | null>(null);
    let disconnecting = $state<number | null>(null);
    let scrollEl = $state<HTMLElement | null>(null);
    let searchTimer: ReturnType<typeof setTimeout> | null = null;
    const emptyHasMore = $derived(media.kind === 'empty' && media.hasMore);
    // Candidate/connected lists are just as source-scoped as media pages. A
    // Wails call cannot be force-cancelled once issued, so only the latest
    // refresh may replace the picker after an account/source change.
    let sourceRefreshVersion = 0;

    function channelError(error: unknown, fallback: string): string {
        const message = error instanceof Error ? error.message : fallback;
        const wait = message.match(/flood[_\s-]*wait\D*(\d+)/i) ?? message.match(/rate limit\D*(\d+)/i);
        return wait ? `Telegram is rate limiting requests. Try again after ${wait[1]} seconds.` : message;
    }

    async function refreshSources({ keepSelection = true }: { keepSelection?: boolean } = {}): Promise<void> {
        const version = ++sourceRefreshVersion;
        loadingSources = true;
        sourceError = '';
        try {
            const [connected, available] = await Promise.all([loadConnected(), loadCandidates()]);
            if (version !== sourceRefreshVersion) return;
            sources = connected.filter((source) => source.connected);
            candidates = available.filter((source) => !source.connected);
            const retained = keepSelection ? sources.find((source) => source.channelId === selected?.channelId) : null;
            selected = retained ?? sources[0] ?? null;
            pager.select(selected);
            media = pager.snapshot();
            if (selected) await loadFirstPage();
        } catch (error) {
            if (version !== sourceRefreshVersion) return;
            sourceError = channelError(error, 'Could not load your Telegram channels.');
        } finally {
            if (version === sourceRefreshVersion) loadingSources = false;
        }
    }

    async function choose(source: ChannelSource): Promise<void> {
        if (source.channelId === selected?.channelId) return;
        selected = source;
        pager.select(source);
        media = pager.snapshot();
        scrollEl?.scrollTo({ top: 0 });
        await loadFirstPage();
    }

    async function loadFirstPage(): Promise<void> {
        media = await pager.load();
    }

    async function loadMore(): Promise<void> {
        media = await pager.load({ append: true });
    }

    function onScroll(): void {
        if (!scrollEl || media.kind !== 'ready' || !media.hasMore) return;
        if (scrollEl.scrollTop + scrollEl.clientHeight >= scrollEl.scrollHeight - 220) void loadMore();
    }

    function updateFilters(): void {
        if (searchTimer) clearTimeout(searchTimer);
        searchTimer = setTimeout(() => {
            pager.setFilter(search, kind);
            media = pager.snapshot();
            scrollEl?.scrollTo({ top: 0 });
            void loadFirstPage();
        }, 220);
    }

    async function addSource(source: ChannelSource): Promise<void> {
        connecting = source.channelId;
        try {
            await connect(source);
            await refreshSources({ keepSelection: false });
            const connected = sources.find((entry) => entry.channelId === source.channelId);
            if (connected) await choose(connected);
        } catch (error) {
            sourceError = channelError(error, 'Could not connect this channel.');
        } finally {
            connecting = null;
        }
    }

    async function removeSource(): Promise<void> {
        if (!selected) return;
        const removed = selected.channelId;
        disconnecting = removed;
        try {
            await disconnect(selected);
            sourceRefreshVersion += 1;
            pager.invalidate();
            selected = null;
            media = pager.snapshot();
            await refreshSources({ keepSelection: false });
        } catch (error) {
            sourceError = channelError(error, 'Could not disconnect this channel.');
        } finally {
            disconnecting = null;
        }
    }

    function itemLabel(item: ChannelMediaItem): string {
        const details = [mediaKindLabel(item), item.size > 0 ? formatBytes(item.size) : '', item.date > 0 ? formatDate(item.date) : ''].filter(Boolean);
        return `${item.name}. ${details.join(', ')}`;
    }

    function mediaKindLabel(item: ChannelMediaItem): string {
        if (item.kind === 'audio') return 'Audio';
        if (item.kind === 'video') return 'Video';
        return 'Media';
    }

    function restriction(item: ChannelMediaItem): string {
        return item.blockReason || 'This Telegram post is unavailable for streaming in TDrive.';
    }

    onMount(() => {
        void refreshSources();
        return () => {
            if (searchTimer) clearTimeout(searchTimer);
            sourceRefreshVersion += 1;
            pager.invalidate();
        };
    });
</script>

<section class="channel-surface" aria-label="Telegram channels">
    <header class="channel-header">
        <div class="channel-heading">
            <span class="channel-kicker"><LinkIcon size={15} strokeWidth={2.2} aria-hidden="true" /> Telegram sources</span>
            <h1>Channels</h1>
            <p>Browse joined channels here. Media remains read-only and stays in Telegram.</p>
        </div>
        <div class="channel-header-actions">
            <IconButton label="Refresh channels" onclick={() => void refreshSources()} disabled={loadingSources}><RefreshCwIcon size={18} /></IconButton>
            {#if onClose}<IconButton label="Close channels" onclick={onClose}><XIcon size={18} /></IconButton>{/if}
        </div>
    </header>

    {#if sourceError}
        <StateView tone="error" title="Channels need attention" body={sourceError}>
            <Button variant="secondary" onclick={() => void refreshSources()}>Try again</Button>
        </StateView>
    {:else if loadingSources}
        <StateView tone="loading" title="Loading joined channels" busy />
    {:else}
        <div class="channel-layout">
            <aside class="channel-picker" aria-label="Connected channels">
                <div class="picker-heading"><span>Connected</span><span>{sources.length}</span></div>
                {#if sources.length}
                    <div class="source-list" role="list">
                        {#each sources as source (source.channelId)}
                            <div role="listitem"><button class:active={source.channelId === selected?.channelId} class="source-row" type="button" aria-current={source.channelId === selected?.channelId ? 'page' : undefined} onclick={() => void choose(source)}>
                                <span class="source-avatar" aria-hidden="true">{source.title.slice(0, 1).toUpperCase()}</span>
                                <span class="source-copy"><span>{source.title}</span>{#if source.username}<small>@{source.username}</small>{/if}</span>
                                {#if source.protected}<LockKeyholeIcon size={15} aria-label="Protected channel" />{/if}
                            </button></div>
                        {/each}
                    </div>
                {:else}
                    <p class="picker-empty">Connect a channel you have already joined to see its media.</p>
                {/if}
                {#if candidates.length}
                    <div class="picker-heading available"><span>Available to connect</span></div>
                    <div class="source-list" role="list">
                        {#each candidates as source (source.channelId)}
                            <div role="listitem"><button class="source-row candidate" type="button" disabled={connecting === source.channelId} onclick={() => void addSource(source)}>
                                <span class="source-avatar" aria-hidden="true">{source.title.slice(0, 1).toUpperCase()}</span>
                                <span class="source-copy"><span>{source.title}</span>{#if source.username}<small>@{source.username}</small>{/if}</span>
                                <PlusIcon size={17} aria-hidden="true" />
                            </button></div>
                        {/each}
                    </div>
                {/if}
            </aside>

            <div bind:this={scrollEl} class="channel-content" onscroll={onScroll}>
                {#if selected}
                    <div class="channel-toolbar">
                        <div class="channel-source-title"><h2>{selected.title}</h2><span>Read-only source</span></div>
                        <div class="toolbar-controls">
                            <label class="channel-search"><SearchIcon size={16} aria-hidden="true" /><input bind:value={search} oninput={updateFilters} maxlength="120" placeholder="Search posts" aria-label="Search channel posts" /></label>
                            <select bind:value={kind} onchange={updateFilters} aria-label="Media type"><option value="all">All media</option><option value="video">Videos</option><option value="audio">Audio</option></select>
                            <Button variant="secondary" disabled={disconnecting === selected.channelId} onclick={() => void removeSource()}><UnplugIcon size={16} /> Disconnect</Button>
                        </div>
                    </div>
                    {#if selected.protected}
                        <div class="channel-notice"><LockKeyholeIcon size={17} aria-hidden="true" /><span>Protected posts open in Telegram. TDrive will not stream or copy them.</span></div>
                    {/if}
                    {#if media.kind === 'loading' && media.items.length === 0}
                        <StateView tone="loading" title="Loading recent media" busy />
                    {:else if media.kind === 'error'}
                        <StateView tone="error" title="Could not load media" body={media.message}><Button variant="secondary" onclick={() => void loadFirstPage()}>Try again</Button></StateView>
                    {:else if media.kind === 'empty'}
                        <StateView title="No matching media" body={emptyHasMore ? 'No match in this page. Load older posts or choose another filter.' : 'Try another filter, or connect a channel with video or audio posts.'}>
                            {#if emptyHasMore}<Button variant="secondary" onclick={() => void loadMore()}>Load older posts</Button>{/if}
                        </StateView>
                    {:else if media.kind === 'ready' || (media.kind === 'loading' && media.items.length > 0)}
                        <div class="media-list" role="list" aria-label={`${selected.title} media`}>
                            {#each media.items as item (item.msgId)}
                                <article class="media-row" role="listitem">
                                    <div class:audio={item.kind === 'audio'} class="media-kind" aria-hidden="true">{#if item.kind === 'audio'}<AudioLinesIcon size={20} />{:else if item.kind === 'video'}<VideoIcon size={20} />{:else}<FileIcon size={20} />{/if}</div>
                                    <div class="media-copy"><h3>{item.name || item.caption || `Telegram message ${item.msgId}`}</h3><p>{[mediaKindLabel(item), item.size > 0 ? formatBytes(item.size) : '', item.date > 0 ? formatDate(item.date) : ''].filter(Boolean).join(' · ')}</p>{#if item.caption && item.caption !== item.name}<span>{item.caption}</span>{/if}</div>
                                    <div class="media-actions">
                                        {#if item.streamable && !item.blockReason}<Button variant="secondary" aria-label={`Play ${itemLabel(item)}`} onclick={() => void play(item, selected!)}><PlayIcon size={16} /> Play</Button>{:else}<span class="media-restricted" title={restriction(item)}><LockKeyholeIcon size={14} /> {restriction(item)}</span>{/if}
                                        {#if item.telegramUrl}<IconButton label={`Open ${item.name || 'post'} in Telegram`} onclick={() => void openTelegram(item.telegramUrl)}><ExternalLinkIcon size={16} /></IconButton>{/if}
                                    </div>
                                </article>
                            {/each}
                        </div>
                        {#if media.kind === 'ready' && media.hasMore}<div class="load-more"><Button variant="secondary" onclick={() => void loadMore()}>Load more</Button></div>{/if}
                    {/if}
                {:else}
                    <StateView title="Choose a channel" body="Select a connected source, or add one from your joined Telegram channels." />
                {/if}
            </div>
        </div>
    {/if}
</section>

<style>
    .channel-surface { min-height: 0; height: 100%; padding: clamp(var(--space-4), 3vw, var(--space-8)); color: var(--text-main); background: var(--color-canvas); }
    .channel-header { display:flex; align-items:flex-start; justify-content:space-between; gap:var(--space-4); padding-bottom:var(--space-5); border-bottom:1px solid var(--border); }
    .channel-kicker { display:flex; align-items:center; gap:var(--space-2); color:var(--accent); font-size:var(--type-xs); font-weight:var(--weight-strong); }
    h1,h2,h3,p { margin:0; } h1 { margin-top:var(--space-1); color:var(--color-text); font-size:clamp(1.45rem, 3vw, 2rem); letter-spacing:-.025em; line-height:1.15; text-wrap:balance; } .channel-heading p { margin-top:var(--space-2); max-width:62ch; color:var(--text-muted); font-size:var(--type-sm); line-height:1.5; }
    .channel-header-actions,.toolbar-controls,.media-actions { display:flex; align-items:center; gap:var(--space-2); }.channel-layout { display:grid; grid-template-columns:minmax(190px, 250px) minmax(0,1fr); min-height:0; height:calc(100% - 102px); }
    .channel-picker { min-height:0; overflow:auto; padding:var(--space-4) var(--space-3) var(--space-4) 0; border-right:1px solid var(--border); }.picker-heading { display:flex; justify-content:space-between; margin:0 var(--space-2) var(--space-2); color:var(--text-muted); font-size:var(--type-xs); font-weight:var(--weight-strong); text-transform:uppercase; letter-spacing:.055em; }.picker-heading.available { margin-top:var(--space-5); }.source-list { display:grid; gap:2px; }.source-row { width:100%; min-width:0; display:grid; grid-template-columns:32px minmax(0,1fr) auto; align-items:center; gap:var(--space-2); padding:var(--space-2); border:0; border-radius:var(--radius-md); color:var(--text-main); background:transparent; text-align:left; cursor:pointer; }.source-row:hover,.source-row.active { background:var(--surface-control); }.source-row.active { color:var(--accent); }.source-row:focus-visible { outline:0; box-shadow:var(--focus-ring); }.source-row:disabled { cursor:wait; opacity:.55; }.source-avatar { width:32px; height:32px; display:grid; place-items:center; border-radius:var(--radius-sm); background:var(--overlay-accent-1); color:var(--accent); font-size:var(--type-sm); font-weight:var(--weight-strong); }.source-copy { min-width:0; display:grid; gap:1px; }.source-copy>span { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; font-size:var(--type-sm); font-weight:var(--weight-semibold); }.source-copy small { overflow:hidden; color:var(--text-muted); font-size:var(--type-xs); text-overflow:ellipsis; white-space:nowrap; }.picker-empty { margin:var(--space-2); color:var(--text-muted); font-size:var(--type-sm); line-height:1.45; }
    .channel-content { min-width:0; min-height:0; overflow:auto; padding:var(--space-4) 0 var(--space-8) var(--space-5); }.channel-toolbar { display:flex; align-items:flex-start; justify-content:space-between; gap:var(--space-4); margin-bottom:var(--space-4); }.channel-source-title h2 { color:var(--color-text); font-size:var(--type-lg); line-height:1.3; }.channel-source-title span { color:var(--text-muted); font-size:var(--type-xs); }.toolbar-controls { flex-wrap:wrap; justify-content:flex-end; }.channel-search { min-width:180px; display:flex; align-items:center; gap:var(--space-2); padding:0 var(--space-3); border:1px solid var(--border); border-radius:var(--radius-md); color:var(--text-muted); background:var(--surface-control); }.channel-search:focus-within { border-color:var(--accent); box-shadow:var(--focus-ring); }.channel-search input { width:100%; min-width:0; height:34px; border:0; outline:0; color:var(--text-main); background:transparent; font-size:var(--type-sm); }.toolbar-controls select { height:36px; padding:0 var(--space-2); border:1px solid var(--border); border-radius:var(--radius-md); color:var(--text-main); background:var(--surface-control); font-size:var(--type-sm); }.channel-notice { display:flex; gap:var(--space-2); align-items:flex-start; margin:0 0 var(--space-3); padding:var(--space-3); border:1px solid color-mix(in srgb,var(--accent) 32%,transparent); border-radius:var(--radius-md); color:var(--text-muted); background:var(--overlay-accent-1); font-size:var(--type-sm); line-height:1.45; }.media-list { display:grid; border-top:1px solid var(--border); }.media-row { display:grid; grid-template-columns:40px minmax(0,1fr) auto; align-items:center; gap:var(--space-3); min-height:68px; padding:var(--space-3) 0; border-bottom:1px solid var(--border); }.media-kind { width:36px; height:36px; display:grid; place-items:center; border-radius:var(--radius-sm); color:var(--accent); background:var(--overlay-accent-1); }.media-kind.audio { color:var(--success); background:color-mix(in srgb,var(--success) 12%,transparent); }.media-copy { min-width:0; }.media-copy h3 { overflow:hidden; color:var(--text-main); font-size:var(--type-sm); font-weight:var(--weight-semibold); line-height:1.35; text-overflow:ellipsis; white-space:nowrap; }.media-copy p,.media-copy span { display:block; overflow:hidden; margin-top:2px; color:var(--text-muted); font-size:var(--type-xs); text-overflow:ellipsis; white-space:nowrap; }.media-restricted { display:flex; align-items:center; gap:4px; max-width:210px; color:var(--text-muted); font-size:var(--type-xs); line-height:1.3; }.load-more { display:flex; justify-content:center; padding:var(--space-5); }
    @media (max-width: 740px) { .channel-surface { padding:var(--space-4); }.channel-layout { grid-template-columns:1fr; height:auto; }.channel-picker { max-height:220px; padding:var(--space-3) 0; border-right:0; border-bottom:1px solid var(--border); }.channel-content { min-height:0; padding:var(--space-4) 0 calc(var(--space-8) + var(--inset-bottom)); }.channel-toolbar { display:grid; gap:var(--space-3); }.toolbar-controls { justify-content:stretch; }.channel-search { flex:1 1 180px; }.media-row { grid-template-columns:36px minmax(0,1fr); align-items:start; }.media-actions { grid-column:2; justify-content:space-between; }.media-restricted { max-width:none; } }
    @media (prefers-reduced-motion:reduce) { .source-row { transition:none; } }
</style>
