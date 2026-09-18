<script lang="ts">
    /**
     * The trash, as one surface for both shells: a centred dialog on the
     * desktop, a bottom sheet on a phone, because ModalShell resolves that per
     * platform. Every row says where it came from and how long is left before
     * the backend purges it -- the countdown is the reason a trash exists.
     *
     * Permanent deletion is irreversible, so both destructive paths raise
     * TrashConfirmModal instead of acting on the row's button.
     */
    import FileIcon from '@lucide/svelte/icons/file';
    import FolderIcon from '@lucide/svelte/icons/folder';
    import RotateCcwIcon from '@lucide/svelte/icons/rotate-ccw';
    import Trash2Icon from '@lucide/svelte/icons/trash-2';
    import Button from '../Button.svelte';
    import IconButton from '../IconButton.svelte';
    import StateView from '../StateView.svelte';
    import ModalShell from '../modals/ModalShell.svelte';
    import {
        EMPTY_TRASH_KEY, askEmptyTrash, askPurgeTrashEntry, closeTrash, loadTrash, restoreEntry,
        trashBusyKey, trashEntries, trashError, trashOpen, trashStatus,
    } from '../../modules/trash/controller';
    import { entryOrigin, purgeCountdown, trashSummary } from './trash-view';

    // Countdowns are relative to one instant per opening. Re-reading the clock
    // per row would let identical rows disagree mid-list, and a dialog is not
    // open long enough for a ticking timer to earn its wakeups.
    let now = $state(Date.now());

    const entries = $derived($trashEntries);
    const busy = $derived($trashBusyKey !== '');
    const summary = $derived(trashSummary(entries));
    const subtitle = $derived(
        summary ? `${summary}. Restore anything here, or let it purge on its own.` : '',
    );

    $effect(() => {
        if ($trashOpen) now = Date.now();
    });

    function close(): void {
        if (busy) return;
        closeTrash();
    }
</script>

<ModalShell
    hostId="trash-modal"
    open={$trashOpen}
    title="Trash"
    titleId="trash-title"
    {subtitle}
    cardClass="trash-card"
    initialFocus="#trash-done"
    restoreFocus="#nav-trash"
    onClose={close}
>
    <div class="trash-body" aria-busy={busy ? 'true' : 'false'}>
        {#if $trashStatus === 'loading'}
            <StateView tone="loading" busy title="Opening the trash…" />
        {:else if $trashStatus === 'error'}
            <StateView tone="error" title="The trash could not be opened" body={$trashError}>
                <Button variant="secondary" size="sm" onclick={() => void loadTrash()}>Try again</Button>
            </StateView>
        {:else if entries.length === 0}
            <StateView
                title="Nothing in the trash"
                body="Anything you delete waits here until its time runs out, so you can put it back."
            />
        {:else}
            <ul class="trash-list" role="list">
                {#each entries as entry (entry.objectId)}
                    {@const countdown = purgeCountdown(entry.purgeAfter, now)}
                    <li class="trash-row" class:is-busy={$trashBusyKey === entry.objectId}>
                        <span class="trash-row-icon" aria-hidden="true">
                            {#if entry.kind === 'folder'}
                                <FolderIcon size={18} strokeWidth={1.9} />
                            {:else}
                                <FileIcon size={18} strokeWidth={1.9} />
                            {/if}
                        </span>
                        <span class="trash-row-copy">
                            <span class="trash-row-name" title={entry.name}>{entry.name}</span>
                            <!-- The countdown never shrinks: on a narrow sheet
                                 the path is the thing worth cutting, not the
                                 one fact the trash exists to report. -->
                            <span class="trash-row-meta">
                                <span class="trash-row-origin">{entryOrigin(entry)}</span>
                                <span class="trash-row-time" class:is-urgent={countdown.urgent}>
                                    {countdown.label}
                                </span>
                            </span>
                        </span>
                        <span class="trash-row-actions">
                            <IconButton
                                label={`Restore ${entry.name}`}
                                size="sm"
                                tone="accent"
                                disabled={busy}
                                onclick={() => void restoreEntry(entry.objectId)}
                            >
                                <RotateCcwIcon size={15} strokeWidth={2} />
                            </IconButton>
                            <IconButton
                                label={`Delete ${entry.name} permanently`}
                                size="sm"
                                tone="danger"
                                disabled={busy}
                                onclick={() => askPurgeTrashEntry(entry)}
                            >
                                <Trash2Icon size={15} strokeWidth={2} />
                            </IconButton>
                        </span>
                    </li>
                {/each}
            </ul>
        {/if}

        {#if $trashError && $trashStatus !== 'error'}
            <p class="trash-error" role="alert">{$trashError}</p>
        {/if}
    </div>

    <!-- The dialog button classes every other modal uses, in the order that
         suits a trash: the safe way out leads, and the irreversible one is
         present but quiet. The phone footer stacks primary-first, so Done
         also ends up nearest the thumb there. -->
    {#snippet actions()}
        <!-- Nothing to empty, so no button sitting there disabled. -->
        {#if entries.length > 0}
            <button
                id="trash-empty"
                class="secondary-btn trash-empty-btn"
                type="button"
                disabled={busy}
                aria-busy={$trashBusyKey === EMPTY_TRASH_KEY ? 'true' : undefined}
                onclick={askEmptyTrash}
            >
                Empty trash
            </button>
        {/if}
        <button id="trash-done" class="primary-btn" type="button" disabled={busy} onclick={close}>Done</button>
    {/snippet}
</ModalShell>

<style>
    /* Destructive, but not the loudest thing in a dialog whose point is
       getting files back: the danger reads in the colour, not in a fill. */
    .trash-empty-btn {
        color: var(--danger);
        border-color: var(--overlay-danger-2);
    }

    .trash-empty-btn:hover:not(:disabled) {
        background: var(--overlay-danger-1);
        border-color: color-mix(in srgb, var(--danger) 46%, transparent);
    }

    .trash-empty-btn:disabled {
        cursor: not-allowed;
        opacity: 0.5;
    }

    .trash-body {
        display: grid;
        gap: var(--space-3);
        min-width: 0;
    }

    .trash-list {
        /* The dialog holds its own height; a long trash scrolls inside it. */
        max-height: min(46vh, 420px);
        overflow-y: auto;
        margin: 0;
        padding: 0;
        list-style: none;
        display: grid;
    }

    .trash-row {
        display: grid;
        grid-template-columns: auto minmax(0, 1fr) auto;
        align-items: center;
        gap: var(--space-3);
        min-height: 44px;
        padding: var(--space-2) 0;
    }

    .trash-row + .trash-row {
        border-top: 1px solid var(--border);
    }

    /* The row whose mutation is in flight stays readable but clearly inert. */
    .trash-row.is-busy {
        opacity: 0.55;
    }

    .trash-row-icon {
        display: grid;
        place-items: center;
        color: var(--text-muted);
    }

    .trash-row-copy {
        display: grid;
        gap: 2px;
        min-width: 0;
    }

    .trash-row-name {
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
        font-size: var(--font-size-sm);
        font-weight: var(--weight-semibold);
    }

    .trash-row-meta {
        display: flex;
        /* Where the path cannot fit beside the countdown, it takes the line
           and the countdown drops below, rather than the size being cut
           mid-number. */
        flex-wrap: wrap;
        column-gap: var(--space-2);
        min-width: 0;
        color: var(--text-muted);
        font-size: var(--font-size-xs);
    }

    .trash-row-origin {
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
    }

    .trash-row-time {
        flex: 0 0 auto;
        white-space: nowrap;
    }

    .trash-row-time.is-urgent {
        color: var(--color-warning);
    }

    .trash-row-actions {
        display: flex;
        gap: var(--space-2);
    }

    .trash-error {
        margin: 0;
        color: var(--danger);
        font-size: var(--font-size-xs);
        line-height: 1.45;
    }

    /* Phone: the sheet scrolls as a whole, rows grow to a comfortable target,
       and the type comes off the mobile scale. */
    :global(html.mobile) .trash-list {
        max-height: none;
    }

    :global(html.mobile) .trash-row {
        min-height: 56px;
    }

    :global(html.mobile) .trash-row-name {
        font-size: var(--mobile-type-body);
    }

    :global(html.mobile) .trash-row-meta,
    :global(html.mobile) .trash-error {
        font-size: var(--mobile-type-meta);
    }

    :global(html.mobile) .trash-row-actions :global(.ui-icon-button) {
        width: 44px;
        height: 44px;
    }

    /* The box is already a 44px target, so the hit-area pseudo-element that
       IconButton grows on phones would only overflow the row. */
    :global(html.mobile) .trash-row-actions :global(.ui-icon-button)::after {
        inset: 0;
    }
</style>
