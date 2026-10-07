<script lang="ts">
    import { untrack } from 'svelte';
    import CheckIcon from '@lucide/svelte/icons/check';
    import PlusIcon from '@lucide/svelte/icons/plus';
    import SearchIcon from '@lucide/svelte/icons/search';
    import LinkIcon from '@lucide/svelte/icons/link';
    import ModalShell from '../modals/ModalShell.svelte';
    import { toAppError } from '../../modules/errors';
    import ChannelAvatar from './ChannelAvatar.svelte';
    import { sourceHandle, sourceKey, sourcePeerLabel, type ChannelSource } from './channel-model';

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
    let publicInput = $state('');
    let publicSource = $state<ChannelSource | null>(null);
    let publicError = $state('');
    let resolvingPublic = $state(false);
    let loadVersion = 0;
    let publicVersion = 0;

    const addedIds = $derived(new Set(added.map(sourceKey)));
    const isAdded = (source: ChannelSource) => addedIds.has(sourceKey(source));
    const candidatesTruncated = $derived(candidates.status === 'ready' && candidates.sources.some((source) => source.candidatesTruncated));

    // Channels still to add lead; the ones already in TDrive sit at the end.
    const visible = $derived.by(() => {
        if (candidates.status !== 'ready') return [];
        const needle = query.trim().toLocaleLowerCase();
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
        publicInput = '';
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
        if (event.key !== 'Enter' || visible.length !== 1) return;
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
        const input = publicInput.trim();
        if (!input || resolvingPublic) return;
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

    function onPublicInput(): void {
        publicVersion += 1;
        publicSource = null;
        publicError = '';
        resolvingPublic = false;
    }

    function onPublicKeydown(event: KeyboardEvent): void {
        if (event.key !== 'Enter') return;
        event.preventDefault();
        void resolveLink();
    }
</script>

<ModalShell
    hostId="channel-picker-modal"
    {open}
    title="Add a source"
    titleId="channel-picker-title"
    subtitle="Choose a chat you can already access, or connect a public channel."
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
        placeholder="Search chats and channels"
            autocomplete="off"
            spellcheck="false"
        aria-label="Search sources"
                aria-controls="channel-picker-list"
        />
    </label>

    <div class="channel-picker-public">
        <label for="channel-public-link">Public channel link</label>
        <div class="channel-picker-public-field">
            <LinkIcon size={16} strokeWidth={2} aria-hidden="true" />
            <input
                id="channel-public-link"
                bind:value={publicInput}
                oninput={onPublicInput}
                onkeydown={onPublicKeydown}
                type="text"
                inputmode="url"
                autocomplete="off"
                spellcheck="false"
                placeholder="@username or t.me/..."
                aria-describedby={publicError ? 'channel-public-link-error' : undefined}
            />
            <button class="secondary-btn channel-picker-check" type="button" disabled={!publicInput.trim() || resolvingPublic || adding !== null} onclick={() => void resolveLink()}>
                {resolvingPublic ? 'Checking' : 'Check link'}
            </button>
        </div>
        <p class="channel-picker-public-help">Checking only verifies access. TDrive never joins channels, sends messages, or starts bots.</p>
        {#if publicError}<p id="channel-public-link-error" class="channel-picker-error" role="alert">{publicError}</p>{/if}
        {#if publicSource}
            {@const publicAdded = isAdded(publicSource)}
            <button class="channel-picker-public-result" type="button" disabled={!publicSource.available || (adding !== null && adding !== sourceKey(publicSource))} onclick={() => { if (publicSource) void choose(publicSource); }}>
                <ChannelAvatar source={publicSource} {loadPhoto} />
                <span class="channel-picker-text">
                    <span class="channel-picker-name">{publicSource.title}</span>
                    <span class="channel-picker-handle">{handle(publicSource)}</span>
                </span>
                <span class="channel-picker-trail">{publicAdded ? 'Open' : publicSource.available ? 'Connect' : 'Unavailable'}</span>
            </button>
        {/if}
    </div>

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
            <div class="channel-picker-empty">No joined sources yet. Your direct messages, bot chats, groups, and channels appear here when Telegram can access them.</div>
        {:else if visible.length === 0}
            <div class="channel-picker-empty">No sources match “{query.trim()}”.</div>
        {:else}
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
        <p class="channel-picker-list-note">Telegram returned the first 1,000 chats from a folder. This search filters those results. The public channel field can check a public @username separately.</p>
    {/if}

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

    .channel-picker-public {
        margin-top: var(--space-3);
    }

    .channel-picker-public > label {
        display: block;
        margin-bottom: var(--space-1);
        color: var(--text-main);
        font-size: var(--type-sm);
        font-weight: var(--weight-semibold);
    }

    .channel-picker-public-field {
        display: flex;
        align-items: center;
        gap: var(--space-2);
        min-width: 0;
        padding: 0 var(--space-2) 0 var(--space-3);
        border: 1px solid var(--border);
        border-radius: var(--radius-md);
        background: var(--bg-dark);
        color: var(--text-muted);
    }

    .channel-picker-public-field:focus-within { border-color: var(--accent); box-shadow: var(--focus-ring); }
    .channel-picker-public-field input { min-width: 0; flex: 1; height: 40px; border: 0; outline: 0; background: transparent; color: var(--text-main); font: inherit; }
    .channel-picker-check { flex: 0 0 auto; min-height: 32px; padding: 0 var(--space-2); font-size: var(--type-xs); }
    .channel-picker-public-help { margin: var(--space-1) 0 0; color: var(--text-muted); font-size: var(--type-xs); line-height: 1.4; }

    .channel-picker-public-result {
        display: flex;
        width: 100%;
        min-height: 52px;
        align-items: center;
        gap: var(--space-3);
        margin-top: var(--space-2);
        padding: var(--space-2);
        border: 1px solid var(--border);
        border-radius: var(--radius-md);
        background: transparent;
        color: var(--text-main);
        font: inherit;
        text-align: left;
        cursor: pointer;
    }
    .channel-picker-public-result:hover:not(:disabled) { background: var(--bg-panel); }
    .channel-picker-public-result:focus-visible { outline: none; box-shadow: var(--focus-ring); }

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

    .channel-picker-list-note {
        margin: calc(-1 * var(--space-2)) var(--space-2) var(--space-3);
        color: var(--text-muted);
        font-size: var(--type-xs);
        line-height: 1.4;
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
