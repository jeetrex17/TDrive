<script lang="ts">
    import { get } from 'svelte/store';
    import { tick } from 'svelte';
    import CheckIcon from '@lucide/svelte/icons/check';
    import FolderIcon from '@lucide/svelte/icons/folder';
    import PenLineIcon from '@lucide/svelte/icons/pen-line';
    import Trash2Icon from '@lucide/svelte/icons/trash-2';
    import { isMobilePlatform } from '../../api';
    import { toAppError } from '../../modules/errors';
    import ModalShell from '../modals/ModalShell.svelte';
    import { deleteSourceGroup, moveSourceToGroup, saveSourceGroup } from './source-organization-api';
    import { sourceKey, type ChannelSource } from './channel-model';
    import {
        closeSourceOrganization,
        groupForSource,
        loadSourceOrganization,
        publishSourceOrganization,
        sourceOrganization,
        sourceOrganizationRequest,
        type SourceGroup,
    } from './source-organization-state';

    let editing = $state<SourceGroup | null>(null);
    let removing = $state<SourceGroup | null>(null);
    let name = $state('');
    let busy = $state(false);
    let error = $state('');

    const request = $derived($sourceOrganizationRequest);
    const source = $derived(request?.source);
    const data = $derived($sourceOrganization.accountId === request?.accountId ? $sourceOrganization : null);
    const currentGroup = $derived(source && data ? groupForSource(source, data) : '');

    $effect(() => {
        if (!request) return;
        editing = null;
        removing = null;
        name = '';
        busy = false;
        error = '';
    });

    function close(): void {
        if (!busy) closeSourceOrganization();
    }

    async function startEditing(group: SourceGroup = { id: '', name: '' }): Promise<void> {
        editing = group;
        name = group.name;
        error = '';
        await tick();
        document.getElementById('source-group-name')?.focus();
    }

    async function startRemoving(group: SourceGroup): Promise<void> {
        removing = group;
        error = '';
        await tick();
        document.getElementById('source-group-cancel-delete')?.focus();
    }

    async function change(run: () => ReturnType<typeof saveSourceGroup>, onSuccess: () => void): Promise<void> {
        const pending = request;
        if (!pending || busy) return;
        busy = true;
        error = '';
        try {
            const result = await run();
            if (get(sourceOrganizationRequest) !== pending) return;
            publishSourceOrganization(pending.accountId, result);
            onSuccess();
        } catch (cause) {
            if (get(sourceOrganizationRequest) === pending) {
                error = toAppError(cause, { source: 'backend' }).message;
            }
        } finally {
            if (get(sourceOrganizationRequest) === pending) busy = false;
        }
    }

    function save(): void {
        const account = request?.accountId;
        const group = editing;
        const title = name.trim();
        if (!account || !group || !title) return;
        void change(() => saveSourceGroup(account, group.id, title), () => { editing = null; });
    }

    function remove(): void {
        const account = request?.accountId;
        const group = removing;
        if (!account || !group) return;
        void change(() => deleteSourceGroup(account, group.id), () => { removing = null; });
    }

    async function restoreMovedSourceFocus(selected: ChannelSource, groupId: string): Promise<void> {
        await tick();
        const sidebar = document.querySelector('.sidebar');
        const row = sidebar?.querySelector<HTMLElement>(`[data-channel-source-key="${CSS.escape(sourceKey(selected))}"]`);
        const toggle = sidebar?.querySelector<HTMLElement>(`[data-channel-group-id="${CSS.escape(groupId)}"]`);
        (row ?? toggle)?.focus();
    }

    function move(id: string): void {
        const selected = source;
        if (!selected) return;
        void change(() => moveSourceToGroup(selected, id), () => {
            closeSourceOrganization();
            if (!isMobilePlatform()) void restoreMovedSourceFocus(selected, id);
        });
    }

    function back(): void {
        editing = null;
        removing = null;
        error = '';
    }
</script>

<ModalShell
    hostId="source-organization-modal"
    open={Boolean(request)}
    title={editing ? (editing.id ? 'Rename group' : 'New group') : removing ? 'Delete group?' : source ? 'Move to group' : 'Source groups'}
    titleId="source-organization-title"
    subtitle={!editing && !removing && source ? source.title : undefined}
    dismissible={!busy}
    onClose={close}
>
    {#if editing}
        <form id="source-group-form" onsubmit={(event) => { event.preventDefault(); save(); }}>
            <label class="field-label" for="source-group-name">Group name</label>
            <input id="source-group-name" type="text" autocomplete="off" maxlength={80} bind:value={name} disabled={busy} />
        </form>
    {:else if removing}
        <p class="group-message">Sources in <strong>{removing.name}</strong> will return to Ungrouped.</p>
    {:else if data?.status === 'ready'}
        {#if source}
            <div class="group-list">
                {#if currentGroup}
                    <button class="group-option" type="button" disabled={busy} onclick={() => move('')}>
                        <span>Ungrouped</span>
                    </button>
                {/if}
                {#each data.groups as group (group.id)}
                    <button
                        class="group-option" type="button" disabled={busy || currentGroup === group.id}
                        aria-label={currentGroup === group.id ? `${group.name}, current group` : undefined}
                        onclick={() => move(group.id)}
                    >
                        <FolderIcon size={16} aria-hidden="true" />
                        <span>{group.name}</span>
                        {#if currentGroup === group.id}
                            <CheckIcon size={16} aria-hidden="true" />
                        {/if}
                    </button>
                {:else}
                    <p class="group-message">Create a group to organize this source.</p>
                {/each}
            </div>
        {:else}
            <ul class="group-list">
                {#each data.groups as group (group.id)}
                    <li class="group-row">
                        <FolderIcon size={16} aria-hidden="true" />
                        <span>{group.name}</span>
                        <button class="group-action" type="button" aria-label={`Rename ${group.name}`} onclick={() => { void startEditing(group); }}>
                            <PenLineIcon size={16} aria-hidden="true" />
                        </button>
                        <button class="group-action" type="button" aria-label={`Delete ${group.name}`} onclick={() => { void startRemoving(group); }}>
                            <Trash2Icon size={16} aria-hidden="true" />
                        </button>
                    </li>
                {:else}
                    <li class="group-message">No groups yet.</li>
                {/each}
            </ul>
        {/if}
    {:else if data?.status === 'error'}
        <p class="group-message" role="alert">Source groups did not load.</p>
        <button class="secondary-btn" type="button" onclick={() => { if (request) void loadSourceOrganization(request.accountId, true); }}>Retry</button>
    {:else}
        <p class="group-message" role="status">Loading source groups…</p>
    {/if}
    {#if error}
        <p class="group-error" role="alert">{error}</p>
    {/if}

    {#snippet actions()}
        {#if editing}
            <button class="secondary-btn" type="button" disabled={busy} onclick={back}>Cancel</button>
            <button class="primary-btn" type="submit" form="source-group-form" disabled={busy || !name.trim()}>
                {editing.id ? 'Save' : 'Create group'}
            </button>
        {:else if removing}
            <button id="source-group-cancel-delete" class="secondary-btn" type="button" disabled={busy} onclick={back}>Cancel</button>
            <button class="primary-btn danger-btn" type="button" disabled={busy} onclick={remove}>Delete group</button>
        {:else}
            <button class="secondary-btn" type="button" disabled={busy} onclick={close}>{source ? 'Cancel' : 'Done'}</button>
            {#if data?.status === 'ready'}
                <button class="primary-btn" type="button" disabled={busy} onclick={() => { void startEditing(); }}>New group</button>
            {/if}
        {/if}
    {/snippet}
</ModalShell>

<style>
    .group-list {
        display: grid;
        gap: var(--space-1);
        max-height: min(40vh, 320px);
        overflow-y: auto;
        margin: 0;
        padding: 0;
        list-style: none;
    }

    .group-option,
    .group-row {
        display: flex;
        align-items: center;
        gap: var(--space-2);
        min-height: 40px;
        padding: 0 var(--space-2);
        border: 0;
        border-radius: var(--radius-sm);
        color: var(--text-main);
        background: var(--color-surface-2);
        text-align: start;
    }

    .group-option > span:first-of-type,
    .group-row > span { flex: 1; min-width: 0; overflow-wrap: anywhere; }

    .group-option:not(:disabled) { cursor: pointer; }
    .group-option:disabled { color: var(--text-muted); }
    .group-option:hover:not(:disabled), .group-action:hover { background: var(--bg-panel); }

    .group-action {
        display: grid;
        place-items: center;
        width: 32px;
        height: 32px;
        flex: 0 0 auto;
        border: 0;
        border-radius: var(--radius-sm);
        color: inherit;
        background: transparent;
        cursor: pointer;
    }

    .group-action:focus-visible,
    .group-option:focus-visible { outline: none; box-shadow: inset var(--focus-ring); }

    .group-message,
    .group-error { margin: var(--space-2) 0; color: var(--text-muted); }
    .group-error { color: var(--danger); }

    :global(html.mobile) .group-option { min-height: var(--touch-target); }
    :global(html.mobile) .group-action { width: var(--touch-target); height: var(--touch-target); }
</style>
