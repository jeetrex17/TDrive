<script lang="ts">
    import ArrowDownIcon from '@lucide/svelte/icons/arrow-down';
    import ArrowUpIcon from '@lucide/svelte/icons/arrow-up';
    import XIcon from '@lucide/svelte/icons/x';
    import TransferItems from './TransferItems.svelte';
    import { isMoving, transferDetailParts, transferPercent, transferPhase, type TransferDetailPart } from './transfer-view';
    import { isUnfinishedTransfer, type TransferEvent } from './notif-store';
    import { isPhotoBackupActivity } from '../../modules/photo-backup/activity';

    /**
     * One transfer in the desktop bell's popover, where each row stands alone on
     * glass and carries its own border and corner.
     *
     * The phone has its own row (ui/mobile/MobileTransferRow). This one used to
     * serve both, gated on the platform in eight places and undone again by most
     * of a screenful of stylesheet overrides; the two surfaces want different
     * shapes and now each states its own.
     *
     * The shapes are all either of them states. What a row *says* -- which state
     * it is in, how full the bar is, which figures are true enough to print --
     * is ui/notifications/transfer-view, shared with the phone, because the two
     * had drifted into disagreeing: this row drew a queued download as a 0% bar
     * reading "0%" while the phone said "Waiting its turn".
     */
    interface Props {
        transfer: TransferEvent;
        /**
         * Cancels everything moving in this row's direction. The bell's rows are
         * its only cancel -- there is no Cancel all beside its section titles --
         * and only three uploads run at once, so a narrower control would leave
         * a twenty-file batch with no way to stop.
         */
        onCancel?: (direction: TransferEvent['direction']) => void;
        /**
         * Stops one file of an aggregate row. The row-wide control above takes
         * the whole batch down; this is how a reader drops the one 4 GB video
         * holding up the other hundred and ninety-nine files.
         */
        onCancelFile?: (key: string) => void;
    }

    let { transfer, onCancel, onCancelFile }: Props = $props();

    const direction = $derived(transfer.direction === 'up' ? 'upload' : 'download');
    const dirLabel = $derived(transfer.direction === 'up' ? 'Uploading' : 'Downloading');

    const phase = $derived(transferPhase(transfer));
    const percent = $derived(transferPercent(transfer));
    const detail = $derived(transferDetailParts(transfer));
    const showProgress = $derived(isMoving(phase));
    const items = $derived(showProgress ? transfer.items ?? [] : []);

    // The stylesheet's four terminal looks, plus the phase itself for anything
    // that wants to tell a paused row from a running one without a class of its
    // own for each.
    const statusClass = $derived(
        phase === 'done' ? 'is-done'
        : phase === 'failed' ? 'is-failed'
        : phase === 'canceled' ? 'is-canceled'
        : phase === 'canceling' ? 'is-canceling'
        : 'is-active',
    );
    const progressLabel = $derived(`${dirLabel} ${transfer.name || 'transfer'}`);

    const canCancel = $derived(
        isUnfinishedTransfer(transfer.status)
        && transfer.status !== 'canceling'
        // The backup queue is the backend's to schedule, so a stop control here
        // would stop nothing.
        && !isPhotoBackupActivity(transfer.id)
        // The download backend can stop only the job it is currently running,
        // and 'active' rather than the running phase because a download still
        // working out what it is downloading is that job. A queued row gets no
        // fake individual cancel control.
        && (transfer.direction === 'up' || transfer.status === 'active'),
    );
    const cancelLabel = $derived(
        transfer.direction === 'down' ? 'Cancel active download' : 'Cancel all uploads',
    );

    /** Figures are set apart from the muted state line; the rate is set smaller. */
    function metaClass(kind: TransferDetailPart['kind']): string {
        return kind === 'rate' ? 'notif-row-speed' : kind === 'figure' ? 'notif-row-size' : 'notif-row-state';
    }

    function cancel(event: MouseEvent): void {
        event.stopPropagation();
        onCancel?.(transfer.direction);
    }
</script>

<div class={`notif-row notif-row-transfer ${statusClass}`} data-phase={phase}>
    <span class="notif-row-icon" data-kind={direction} aria-hidden="true">
        {#if transfer.direction === 'up'}
            <ArrowUpIcon size={14} strokeWidth={2} aria-hidden="true" />
        {:else}
            <ArrowDownIcon size={14} strokeWidth={2} aria-hidden="true" />
        {/if}
    </span>
    <div class="notif-row-body">
        <div class="notif-row-title" title={transfer.name}>{transfer.name || dirLabel}</div>
        {#if transfer.note}
            <!-- Why it failed, or on a phone where it landed: the one sentence
                 the row keeps after the toast that said it has gone. -->
            <div class="notif-row-note">{transfer.note}</div>
        {/if}
        <!-- A progressbar reports state when queried or focused, without a
             live region that would announce each transfer tick. -->
        {#if showProgress}
            <div
                class="notif-row-progress"
                class:is-indeterminate={percent === null}
                role="progressbar"
                aria-label={progressLabel}
                aria-valuemin="0"
                aria-valuemax="100"
                aria-valuenow={percent === null ? undefined : Math.round(percent)}
            >
                <!-- No width where there is no figure: a folder still being
                     walked has no fraction to be, and a bar parked at 0% reads
                     as a transfer that has stalled. -->
                <div
                    class="notif-row-progress-fill"
                    style={percent === null ? undefined : `width:${percent}%`}
                    aria-hidden="true"
                ></div>
            </div>
        {/if}
    </div>
    <!-- Read down: how many files, how far, how much, how long, how fast, in the
         order transfer-view puts them -- the count leads because on an aggregate
         it is the figure the reader is actually tracking, the rate trails
         because it says the least. The column stacks what the phone joins into
         one line; both leave out what is not true rather than spelling it zero. -->
    <div class="notif-row-meta">
        <!-- Keyed by position: the parts are a fixed reading order, not a list
             of things with identities, and two of them can read alike. -->
        {#each detail as part, index (index)}
            <div class={metaClass(part.kind)}>{part.text}</div>
        {/each}
    </div>
    {#if canCancel}
        <button
            class="notif-row-cancel"
            type="button"
            data-cancel-dir={transfer.direction}
            aria-label={cancelLabel}
            title={cancelLabel}
            onclick={cancel}
        >
            <XIcon size={12} strokeWidth={2} aria-hidden="true" />
        </button>
    {/if}
    {#if items.length > 0}
        <div class="notif-row-items">
            <TransferItems {items} active={transfer.itemsActive ?? items.length} onCancel={onCancelFile} />
        </div>
    {/if}
</div>

<style>
    /* The one state the shared stylesheet has no word for: a transfer that has
       started with nothing measurable yet -- a folder still being walked. The
       bar says the work is running rather than standing at a figure it does not
       have, which is what a full-width fill with no percentage would claim.

       Local to the row because it belongs to markup only this row has; the rest
       of it is styles/activity.css, which the popover shares. */
    .is-indeterminate .notif-row-progress-fill {
        width: 38%;
        transition: none;
        animation: notif-row-indeterminate 1.25s var(--ease-standard) infinite;
    }
    @keyframes notif-row-indeterminate {
        from { transform: translateX(-105%); }
        to { transform: translateX(268%); }
    }
    @media (prefers-reduced-motion: reduce) {
        .is-indeterminate .notif-row-progress-fill { width: 100%; animation: none; opacity: 0.4; }
    }
</style>
