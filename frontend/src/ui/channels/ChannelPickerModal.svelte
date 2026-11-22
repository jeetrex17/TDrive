<script lang="ts">
    import { untrack } from 'svelte';
    import CheckIcon from '@lucide/svelte/icons/check';
    import PlusIcon from '@lucide/svelte/icons/plus';
    import SearchIcon from '@lucide/svelte/icons/search';
    import LinkIcon from '@lucide/svelte/icons/link';
    import ModalShell from '../modals/ModalShell.svelte';
    import { toAppError } from '../../modules/errors';
    import ChannelAvatar from './ChannelAvatar.svelte';
    import { publicLookup, sourceHandle, sourceKey, sourcePeerLabel, type ChannelSource } from './channel-model';

    interface Props {
        open: boolean;
        /** Joined direct messages, bots, groups and channels, including archived peers. */
        loadCandidates: () => Promise<ChannelSource[]>;
        /** Checks a public @username or t.me link without joining Telegram. */
        resolvePublic: (input: string) => Promise<ChannelSource>;
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

    let { open, loadCandidates, resolvePublic, added, loadPhoto, onAdd, onOpen, onClose }: Props = $props();

    type Candidates =
        | { status: 'loading' }
        | { status: 'ready'; sources: readonly ChannelSource[] }
        | { status: 'error'; error: unknown };

    let candidates = $state<Candidates>({ status: 'loading' });
    let query = $state('');
    let adding = $state<string | null>(null);
    let addError = $state('');
    let publicSource = $state<ChannelSource | null>(null);
    let publicError = $state('');
    let resolvingPublic = $state(false);
    let loadVersion = 0;
    let publicVersion = 0;

    const addedIds = $derived(new Set(added.map(sourceKey)));
    const isAdded = (source: ChannelSource) => addedIds.has(sourceKey(source));
    const candidatesTruncated = $derived(candidates.status === 'ready' && candidates.sources.some((source) => source.candidatesTruncated));
    // A pasted link or @name looks the channel up on Telegram; anything else
    // only filters the chats this account is in.
    const lookup = $derived(publicLookup(query));
    const canResolvePublic = $derived(lookup !== null && !resolvingPublic && adding === null);

    // Channels still to add lead; the ones already in TDrive sit at the end.
    const visible = $derived.by(() => {
        if (candidates.status !== 'ready') return [];
        const needle = (lookup ?? query).trim().replace(/^@/, '').toLocaleLowerCase();
        const matches = needle
            ? candidates.sources.filter((source) => source.title.toLocaleLowerCase().includes(needle)
                || source.username.toLocaleLowerCase().includes(needle)
                || sourcePeerLabel(source).toLocaleLowerCase().includes(needle))
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
        publicSource = null;
        publicError = '';
        publicVersion += 1;
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
        if (!source.available) {
            addError = `${source.title} is unavailable to this account.`;
            return;
        }
        adding = sourceKey(source);
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
        if (event.key !== 'Enter') return;
        if (lookup !== null) {
            event.preventDefault();
            if (publicSource) void choose(publicSource);
            else void resolveLink();
            return;
        }
        if (visible.length !== 1) return;
        event.preventDefault();
        void choose(visible[0]);
    }

    function handle(source: ChannelSource): string {
        const details = [sourceHandle(source), sourcePeerLabel(source)];
        if (source.protected) details.push('Protected');
        if (!source.available) details.push('Unavailable');
        return details.filter((value, index, values) => values.indexOf(value) === index).join(' · ');
    }

    async function resolveLink(): Promise<void> {
        if (!canResolvePublic) return;
        const input = query.trim();
        const version = ++publicVersion;
        resolvingPublic = true;
        publicError = '';
        publicSource = null;
        try {
            const resolved = await resolvePublic(input);
            if (resolved.peerKind !== 'channel') throw new Error('That link does not identify a public channel.');
            if (version === publicVersion) publicSource = { ...resolved, publicUsername: resolved.username };
        } catch (error) {
            if (version === publicVersion) publicError = toAppError(error, { source: 'backend' }).message;
        } finally {
            if (version === publicVersion) resolvingPublic = false;
        }
    }

    // Typing again drops the last lookup, which answered for other text.
    function onSearchInput(): void {
        publicVersion += 1;
        publicSource = null;
        publicError = '';
        resolvingPublic = false;
    }
</script>

<ModalShell
    hostId="channel-picker-modal"
    {open}
    title="Add a source"
    titleId="channel-picker-title"
    subtitle="A chat you're in, or any public channel."
    cardClass="channel-picker-card"
    initialFocus="#channel-picker-search"
    onClose={close}
>
    <label class="search-field channel-picker-search">
        <SearchIcon size={16} strokeWidth={2} aria-hidden="true" />
        <input
            id="channel-picker-search"
            bind:value={query}
            oninput={onSearchInput}
            onkeydown={onSearchKeydown}
            type="text"
            placeholder="Search chats or paste a t.me link"
            autocomplete="off"
            spellcheck="false"
            aria-label="Search chats or paste a t.me link"
            aria-controls="channel-picker-list"
            aria-invalid={publicError ? 'true' : undefined}
            aria-describedby={publicError ? 'channel-picker-lookup-error' : undefined}
        />
    </label>

    {#if addError}
        <p class="channel-picker-error" role="alert">{addError}</p>
    {/if}

    <div id="channel-picker-list" class="channel-picker-list" aria-busy={candidates.status === 'loading'}>
        {#if lookup !== null}
            {#if publicSource}
                {@const publicAdded = isAdded(publicSource)}
                {@const publicBusy = adding === sourceKey(publicSource)}
                <button
                    class="channel-picker-row channel-picker-public-result"
                    type="button"
                    disabled={!publicSource.available || (adding !== null && !publicBusy)}
                    aria-busy={publicBusy}
                    aria-label={publicBusy ? `Connecting ${publicSource.title}` : publicAdded ? `Open ${publicSource.title}, already connected` : publicSource.available ? `Connect ${publicSource.title}` : `${publicSource.title} is unavailable`}
                    onclick={() => { if (publicSource) void choose(publicSource); }}
                >
                    <ChannelAvatar source={publicSource} {loadPhoto} />
                    <span class="channel-picker-text">
                        <span class="channel-picker-name">{publicSource.title}</span>
                        <span class="channel-picker-handle">{handle(publicSource)}</span>
                    </span>
                    <span class="channel-picker-trail" aria-hidden="true">
                        {#if publicBusy}
                            <span class="channel-picker-spinner"></span>
                        {:else if publicAdded}
                            <CheckIcon size={14} strokeWidth={2.5} />Connected
                        {:else if publicSource.available}
                            <span class="channel-picker-plus"><PlusIcon size={14} strokeWidth={2.5} /></span>
                        {:else}
                            Unavailable
                        {/if}
                    </span>
                </button>
            {:else}
                <button
                    class="channel-picker-row channel-picker-lookup"
                    type="button"
                    disabled={!canResolvePublic}
                    aria-busy={resolvingPublic}
                    onclick={() => void resolveLink()}
                >
                    <span class="channel-picker-lookup-icon" aria-hidden="true">
                        {#if resolvingPublic}<span class="channel-picker-spinner"></span>{:else}<LinkIcon size={16} strokeWidth={2} />{/if}
                    </span>
                    <span class="channel-picker-text">
                        <span class="channel-picker-name">{resolvingPublic ? `Looking up ${lookup}…` : `Look up ${lookup}`}</span>
                        <span class="channel-picker-handle">Public channel on Telegram</span>
                    </span>
                    {#if !resolvingPublic}<kbd class="channel-picker-key" aria-hidden="true">↵</kbd>{/if}
                </button>
                {#if publicError}<p id="channel-picker-lookup-error" class="channel-picker-error" role="alert">{publicError}</p>{/if}
            {/if}
        {/if}

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
            {#if lookup === null}
                <div class="channel-picker-empty">No joined sources yet. Your direct messages, bot chats, groups, and channels appear here when Telegram can access them. Paste a t.me link to add a public channel.</div>
            {/if}
        {:else if visible.length === 0}
            {#if lookup === null}
                <div class="channel-picker-empty">No chats match “{query.trim()}”. Paste a t.me link or @name to look up a public channel.</div>
            {/if}
        {:else}
            {#if lookup !== null}<div class="channel-picker-section" aria-hidden="true">Your chats</div>{/if}
            {#each visible as source (sourceKey(source))}
                {@const busy = adding === sourceKey(source)}
                {@const inTDrive = isAdded(source)}
                <button
                    class="channel-picker-row"
                    class:is-added={inTDrive}
                    type="button"
                    disabled={!source.available || (adding !== null && !busy)}
                    aria-busy={busy}
                    aria-label={inTDrive ? `Open ${source.title}, already connected` : source.available ? `Connect ${source.title}` : `${source.title} is unavailable`}
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
                            <CheckIcon size={14} strokeWidth={2.5} />Connected
                        {:else if !source.available}
                            Unavailable
                        {:else}
                            <span class="channel-picker-plus"><PlusIcon size={14} strokeWidth={2.5} /></span>
                        {/if}
                    </span>
                </button>
            {/each}
        {/if}
    </div>

    {#if candidatesTruncated}
        <p class="channel-picker-list-note">Telegram returned the first 1,000 chats from a folder. This search filters those results; a pasted @name or t.me link is looked up separately.</p>
    {/if}

    {#snippet actions()}
        <p class="channel-picker-footnote">Looking up a link won't join the channel or send messages.</p>
        <button class="secondary-btn" type="button" disabled={adding !== null} onclick={close}>Cancel</button>
    {/snippet}
</ModalShell>

<style>
    :global(.channel-picker-card) {
        max-width: 480px;
    }

    /* Pinned from the top like a command palette, so the search box stays
       put while the results below it grow and shrink. */
    :global(html:not(.mobile) #channel-picker-modal.modal-overlay) {
        align-items: flex-start;
        padding-top: 14vh;
    }

    .channel-picker-search {
        height: 40px;
        padding: 0 var(--space-3);
        background: var(--color-surface-1);
    }

    .channel-picker-search:has(input[aria-invalid='true']) { border-color: var(--color-danger); }

    .channel-picker-search input,
    .channel-picker-search input:focus { font-size: var(--type-base); }

    .channel-picker-error {
        margin: var(--space-1) var(--space-2) var(--space-2);
        color: var(--color-danger);
        font-size: var(--type-sm);
        line-height: 1.4;
    }

    /* The list grows with its results, so a short search leaves no empty
       well under it, and scrolls past about eight rows. */
    .channel-picker-list {
        display: flex;
        flex-direction: column;
        gap: 2px;
        min-height: 64px;
        max-height: min(420px, 52vh);
        margin: var(--space-3) calc(-1 * var(--space-2)) var(--space-3);
        padding: 0 var(--space-2);
        overflow-y: auto;
    }

    .channel-picker-row {
        --avatar-size: 32px;
        flex: 0 0 auto;
        display: flex;
        align-items: center;
        gap: var(--space-3);
        min-height: 48px;
        padding: var(--space-2);
        border: 0;
        border-radius: var(--radius-sm);
        background: transparent;
        color: var(--color-text);
        font: inherit;
        text-align: left;
        cursor: pointer;
        transition: background-color var(--motion-fast) var(--ease-standard);
    }

    @media (hover: hover) {
        .channel-picker-row:hover:not(:disabled) { background: var(--overlay-neutral-1); }
    }

    .channel-picker-row:focus-visible { outline: none; box-shadow: var(--focus-ring); }
    .channel-picker-row:disabled { cursor: default; opacity: 0.5; }
    .channel-picker-row[aria-busy='true'] { opacity: 1; }
    .channel-picker-row.is-placeholder { cursor: default; }

    /* The loading rows hold the avatar's place. */
    .channel-picker-avatar {
        flex: 0 0 auto;
        width: 32px;
        height: 32px;
        border-radius: var(--radius-pill);
    }

    .channel-picker-lookup-icon {
        flex: 0 0 auto;
        display: grid;
        width: 32px;
        height: 32px;
        place-items: center;
        border-radius: var(--radius-pill);
        background: var(--color-current-bg);
        color: var(--color-current-text);
    }

    .channel-picker-lookup-icon .channel-picker-spinner { margin: 0; }

    .channel-picker-key {
        flex: 0 0 auto;
        min-width: 22px;
        padding: 1px 6px;
        border: 1px solid var(--color-border-soft);
        border-radius: var(--radius-xs);
        color: var(--color-text-muted);
        font: inherit;
        font-size: var(--type-xs);
        text-align: center;
    }

    .channel-picker-section {
        margin: var(--space-2) var(--space-2) 2px;
        color: var(--color-text-muted);
        font-size: var(--type-xs);
        font-weight: var(--weight-medium);
    }

    .channel-picker-text {
        flex: 1 1 auto;
        min-width: 0;
        display: flex;
        flex-direction: column;
        gap: 2px;
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
        font-size: var(--type-sm);
        font-weight: var(--weight-medium);
    }

    .channel-picker-handle {
        color: var(--color-text-muted);
        font-size: var(--type-xs);
    }

    .channel-picker-trail {
        flex: 0 0 auto;
        display: inline-flex;
        align-items: center;
        gap: 4px;
        color: var(--color-text-muted);
        font-size: var(--type-xs);
        font-weight: var(--weight-medium);
    }

    .channel-picker-plus {
        display: grid;
        width: 24px;
        height: 24px;
        place-items: center;
        border: 1px solid var(--color-border-soft);
        border-radius: var(--radius-pill);
        color: var(--color-text);
        transition:
            background-color var(--motion-fast) var(--ease-standard),
            border-color var(--motion-fast) var(--ease-standard),
            color var(--motion-fast) var(--ease-standard);
    }

    .channel-picker-row:hover .channel-picker-plus,
    .channel-picker-row:focus-visible .channel-picker-plus {
        border-color: var(--color-action);
        background: var(--color-action);
        color: var(--color-on-action);
    }

    .channel-picker-spinner {
        width: 16px;
        height: 16px;
        margin: 4px;
        border: 2px solid var(--color-border-soft);
        border-top-color: var(--color-accent);
        border-radius: var(--radius-pill);
        animation: channel-picker-spin 720ms linear infinite;
    }

    .channel-picker-empty {
        padding: var(--space-6) var(--space-4);
        color: var(--color-text-muted);
        font-size: var(--type-sm);
        line-height: 1.5;
        text-align: center;
        text-wrap: pretty;
    }

    .channel-picker-list-note {
        margin: calc(-1 * var(--space-1)) var(--space-2) var(--space-3);
        color: var(--color-text-muted);
        font-size: var(--type-xs);
        line-height: 1.4;
    }

    .channel-picker-footnote {
        flex: 1 1 auto;
        min-width: 0;
        margin: 0;
        color: var(--color-text-muted);
        font-size: var(--type-xs);
        line-height: 1.4;
    }

    @keyframes channel-picker-spin {
        to { transform: rotate(360deg); }
    }

    /* The sheet scrolls its body itself and sizes it to the screen. */
    :global(html.mobile) .channel-picker-list {
        max-height: none;
        min-height: 280px;
        margin-bottom: 0;
        overflow: visible;
    }

    :global(html.mobile) .channel-picker-search input { font-size: max(16px, var(--type-base)); }
    :global(html.mobile) .channel-picker-row { --avatar-size: 36px; min-height: 60px; }
    :global(html.mobile) .channel-picker-key { display: none; }
    :global(html.mobile) .channel-picker-footnote { text-align: center; }

    @media (prefers-reduced-motion: reduce) {
        .channel-picker-row,
        .channel-picker-plus { transition: none; }

        .channel-picker-spinner { animation: none; }
    }
</style>
