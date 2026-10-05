<script lang="ts">
    import { untrack } from 'svelte';
    import CheckIcon from '@lucide/svelte/icons/check';
    import PlusIcon from '@lucide/svelte/icons/plus';
    import SearchIcon from '@lucide/svelte/icons/search';
    import ModalShell from '../modals/ModalShell.svelte';
    import { toAppError } from '../../modules/errors';
    import ChannelAvatar from './ChannelAvatar.svelte';
    import type { ChannelSource } from './channel-model';

    interface Props {
        open: boolean;
        /** Every broadcast channel the account has joined, added or not. */
        loadCandidates: () => Promise<ChannelSource[]>;
        /**
         * The channels in TDrive now, as the sidebar lists them. They decide
         * what reads as added, since a list kept from an earlier open may not.
         */
        added: readonly ChannelSource[];
        loadPhoto: (source: ChannelSource) => Promise<string>;
        onAdd: (source: ChannelSource) => Promise<void>;
        onOpen: (source: ChannelSource) => void;
        onClose: () => void;
    }

    let { open, loadCandidates, added, loadPhoto, onAdd, onOpen, onClose }: Props = $props();

    type Candidates =
        | { status: 'loading' }
        | { status: 'ready'; sources: readonly ChannelSource[] }
        | { status: 'error'; error: unknown };

    let candidates = $state<Candidates>({ status: 'loading' });
    let query = $state('');
    let adding = $state<number | null>(null);
    let addError = $state('');
    let loadVersion = 0;

    const addedIds = $derived(new Set(added.map((source) => source.channelId)));
    const isAdded = (source: ChannelSource) => addedIds.has(source.channelId);

    // Channels still to add lead; the ones already in TDrive sit at the end.
    const visible = $derived.by(() => {
        if (candidates.status !== 'ready') return [];
        const needle = query.trim().toLocaleLowerCase();
        const matches = needle
            ? candidates.sources.filter((source) => source.title.toLocaleLowerCase().includes(needle)
                || source.username.toLocaleLowerCase().includes(needle))
            : candidates.sources;
        return [...matches.filter((source) => !isAdded(source)), ...matches.filter(isAdded)];
    });

    // Listing joined channels walks every Telegram dialog, so it happens only
    // while the picker is open, never on the way into a channel. Opened again,
    // the picker shows the last list until the fresh one lands.
    async function load(): Promise<void> {
        const version = ++loadVersion;
        if (candidates.status !== 'ready') candidates = { status: 'loading' };
        try {
            const sources = await loadCandidates();
            if (version === loadVersion) candidates = { status: 'ready', sources };
        } catch (error) {
            if (version === loadVersion && candidates.status !== 'ready') candidates = { status: 'error', error };
        }
    }

    $effect(() => {
        if (!open) return;
        query = '';
        adding = null;
        addError = '';
        // Only opening reloads. load() reads the list it replaces, which
        // would otherwise make every answer reload again.
        untrack(() => void load());
        return () => {
            loadVersion += 1;
        };
    });

    function close(): void {
        if (adding === null) onClose();
    }

    async function choose(source: ChannelSource): Promise<void> {
        if (adding !== null) return;
        if (isAdded(source)) {
            onOpen(source);
            onClose();
            return;
        }
        adding = source.channelId;
        addError = '';
        try {
            await onAdd(source);
            adding = null;
            onClose();
        } catch (error) {
            adding = null;
            addError = `${source.title} could not be added. ${toAppError(error, { source: 'backend' }).message}`;
        }
    }

    function onSearchKeydown(event: KeyboardEvent): void {
        if (event.key !== 'Enter' || visible.length !== 1) return;
        event.preventDefault();
        void choose(visible[0]);
    }

    function handle(source: ChannelSource): string {
        const name = source.username ? `@${source.username}` : 'Private channel';
        return source.protected ? `${name} · Protected` : name;
    }
</script>

<ModalShell
    hostId="channel-picker-modal"
    {open}
    title="Add a channel"
    titleId="channel-picker-title"
    subtitle="Choose from the channels you've joined on Telegram."
    cardClass="channel-picker-card"
    initialFocus="#channel-picker-search"
    onClose={close}
>
    <label class="search-field channel-picker-search">
        <SearchIcon size={16} strokeWidth={2} aria-hidden="true" />
        <input
            id="channel-picker-search"
            bind:value={query}
            onkeydown={onSearchKeydown}
            type="text"
            placeholder="Search channels"
            autocomplete="off"
            spellcheck="false"
            aria-label="Search channels"
            aria-controls="channel-picker-list"
        />
    </label>

    {#if addError}
        <p class="channel-picker-error" role="alert">{addError}</p>
    {/if}

    <div id="channel-picker-list" class="channel-picker-list" aria-busy={candidates.status === 'loading'}>
        {#if candidates.status === 'loading'}
            {#each [58, 42, 66, 36, 50] as width, index (index)}
                <div class="channel-picker-row is-placeholder" aria-hidden="true">
                    <span class="channel-picker-avatar file-state-skeleton-icon"></span>
                    <span class="channel-picker-text">
                        <span class="file-state-skeleton-bar" style:--skeleton-width={`${width}%`}></span>
                        <span class="file-state-skeleton-bar" style:--skeleton-width="28%"></span>
                    </span>
                </div>
            {/each}
        {:else if candidates.status === 'error'}
            <div class="channel-picker-empty" role="alert">
                {toAppError(candidates.error, { source: 'backend' }).message}
                <button class="link-button" type="button" onclick={() => void load()}>Try again</button>
            </div>
        {:else if candidates.sources.length === 0}
            <div class="channel-picker-empty">You haven't joined any channels yet. Join one in Telegram, then add it here.</div>
        {:else if visible.length === 0}
            <div class="channel-picker-empty">No channels match “{query.trim()}”.</div>
        {:else}
            {#each visible as source (source.channelId)}
                {@const busy = adding === source.channelId}
                {@const inTDrive = isAdded(source)}
                <button
                    class="channel-picker-row"
                    class:is-added={inTDrive}
                    type="button"
                    disabled={adding !== null && !busy}
                    aria-busy={busy}
                    aria-label={inTDrive ? `Open ${source.title}, already added` : `Add ${source.title}`}
                    onclick={() => void choose(source)}
                >
                    <ChannelAvatar {source} {loadPhoto} />
                    <span class="channel-picker-text">
                        <span class="channel-picker-name">{source.title}</span>
                        <span class="channel-picker-handle">{handle(source)}</span>
                    </span>
                    <span class="channel-picker-trail" aria-hidden="true">
                        {#if busy}
                            <span class="channel-picker-spinner"></span>
                        {:else if inTDrive}
                            <CheckIcon size={14} strokeWidth={2.5} />Added
                        {:else}
                            <span class="channel-picker-plus"><PlusIcon size={14} strokeWidth={2.5} /></span>
                        {/if}
                    </span>
                </button>
            {/each}
        {/if}
    </div>

    {#snippet actions()}
        <button class="secondary-btn" type="button" disabled={adding !== null} onclick={close}>Cancel</button>
    {/snippet}
</ModalShell>

<style>
    :global(.channel-picker-card) {
        max-width: 460px;
    }

    .channel-picker-search {
        height: 40px;
        padding: 0 var(--space-3);
        background: var(--bg-dark);
    }

    .channel-picker-search input,
    .channel-picker-search input:focus { font-size: var(--type-base); }

    .channel-picker-error {
        margin-top: var(--space-3);
        color: var(--danger);
        font-size: var(--type-sm);
        line-height: 1.4;
    }

    .channel-picker-list {
        display: flex;
        flex-direction: column;
        gap: 2px;
        height: min(336px, 46vh);
        margin: var(--space-3) calc(-1 * var(--space-2)) var(--space-4);
        padding: 0 var(--space-2);
        overflow-y: auto;
    }

    .channel-picker-row {
        --avatar-size: 36px;
        flex: 0 0 auto;
        display: flex;
        align-items: center;
        gap: var(--space-3);
        min-height: 52px;
        padding: var(--space-2);
        border: 0;
        border-radius: var(--radius-md);
        background: transparent;
        color: var(--text-main);
        font: inherit;
        text-align: left;
        cursor: pointer;
        transition: background-color var(--motion-fast) var(--ease-standard);
    }

    .channel-picker-row:hover:not(:disabled) { background: var(--bg-panel); }
    .channel-picker-row:focus-visible { outline: none; box-shadow: var(--focus-ring); }
    .channel-picker-row:disabled { cursor: default; opacity: 0.5; }
    .channel-picker-row[aria-busy='true'] { opacity: 1; }
    .channel-picker-row.is-placeholder { cursor: default; }

    /* The loading rows hold the avatar's place. */
    .channel-picker-avatar {
        flex: 0 0 auto;
        width: 36px;
        height: 36px;
        border-radius: var(--radius-pill);
    }

    .channel-picker-text {
        flex: 1 1 auto;
        min-width: 0;
        display: flex;
        flex-direction: column;
        gap: 3px;
    }

    .is-placeholder .channel-picker-text { gap: 8px; }

    .channel-picker-name,
    .channel-picker-handle {
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
    }

    .channel-picker-name {
        color: var(--color-text);
        font-size: var(--type-base);
        font-weight: var(--weight-semibold);
    }

    .channel-picker-handle {
        color: var(--text-muted);
        font-size: var(--type-xs);
    }

    .channel-picker-trail {
        flex: 0 0 auto;
        display: inline-flex;
        align-items: center;
        gap: 4px;
        color: var(--text-muted);
        font-size: var(--type-xs);
        font-weight: var(--weight-semibold);
    }

    .channel-picker-plus {
        display: grid;
        width: 26px;
        height: 26px;
        place-items: center;
        border: 1px solid var(--border);
        border-radius: var(--radius-pill);
        color: var(--text-main);
        transition:
            background-color var(--motion-fast) var(--ease-standard),
            border-color var(--motion-fast) var(--ease-standard),
            color var(--motion-fast) var(--ease-standard);
    }

    .channel-picker-row:hover .channel-picker-plus,
    .channel-picker-row:focus-visible .channel-picker-plus {
        border-color: var(--accent);
        background: var(--accent);
        color: var(--color-on-accent);
    }

    .channel-picker-spinner {
        width: 16px;
        height: 16px;
        margin: 5px;
        border: 2px solid var(--border);
        border-top-color: var(--accent);
        border-radius: var(--radius-pill);
        animation: channel-picker-spin 720ms linear infinite;
    }

    .channel-picker-empty {
        padding: var(--space-6) var(--space-4);
        color: var(--text-muted);
        font-size: var(--type-sm);
        line-height: 1.5;
        text-align: center;
        text-wrap: pretty;
    }

    @keyframes channel-picker-spin {
        to { transform: rotate(360deg); }
    }

    /* The sheet scrolls its body itself and sizes it to the screen. */
    :global(html.mobile) .channel-picker-list {
        height: auto;
        min-height: 280px;
        margin-bottom: 0;
        overflow: visible;
    }

    :global(html.mobile) .channel-picker-search input { font-size: 16px; }
    :global(html.mobile) .channel-picker-row { min-height: 60px; }

    @media (prefers-reduced-motion: reduce) {
        .channel-picker-row,
        .channel-picker-plus { transition: none; }

        .channel-picker-spinner { animation: none; }
    }
</style>
