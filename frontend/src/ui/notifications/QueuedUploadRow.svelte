<script lang="ts">
    import ClockIcon from '@lucide/svelte/icons/clock';
    import XIcon from '@lucide/svelte/icons/x';
    import type { UploadActivity } from './upload-activity-store';
    import { batchLabel, fileCountLabel, isBatchPaused } from './activity-model';
    import { removeQueuedUpload } from '../../modules/upload-queue';
    import { switchActiveChannel } from '../../modules/channels';

    interface Props { batch: UploadActivity; activeDriveId: number | null; onOpen: () => void; mobile?: boolean }
    let { batch, activeDriveId, onOpen, mobile = false }: Props = $props();
    const paused = $derived(isBatchPaused(batch, activeDriveId));
</script>

<div class="notif-row queued-upload-row" class:mobile role="listitem">
    <span class="notif-row-icon" aria-hidden="true"><ClockIcon size={14} strokeWidth={2} /></span>
    <div class="notif-row-body">
        <div class="notif-row-title"><button class="queued-upload-name" type="button" onclick={onOpen}>{batch.name}</button></div>
        <div class="notif-row-sub" class:paused>{paused ? batchLabel(batch, [], activeDriveId) : `Queued · ${fileCountLabel(batch.fileCount)} · ${batch.destination}`}</div>
    </div>
    <div class="queued-upload-actions">
        {#if paused}
            <button class="queued-upload-open" type="button" onclick={() => void switchActiveChannel(batch.driveId)}>Open {batch.driveTitle}</button>
        {/if}
        <button class="notif-row-cancel" type="button" aria-label={`Remove ${batch.name} from queue`} onclick={() => removeQueuedUpload(batch.id)}>
            <XIcon size={14} strokeWidth={2} aria-hidden="true" />
        </button>
    </div>
</div>

<style>
    .queued-upload-row { grid-template-columns: 22px minmax(0, 1fr) auto; }
    .notif-row-icon { color: var(--color-text-muted); }
    .queued-upload-name { max-width: 100%; padding: 0; border: 0; background: transparent; color: inherit; font: inherit; text-align: left; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; cursor: pointer; }
    .notif-row-sub.paused { color: var(--color-warning); }
    .queued-upload-actions { display: flex; align-items: center; gap: var(--space-1); }
    .queued-upload-open { min-height: 26px; padding: 3px 9px; border: 1px solid var(--overlay-accent-2); border-radius: var(--radius-sm); background: var(--overlay-accent-1); color: var(--color-accent-text); font: inherit; font-size: 0.76rem; font-weight: var(--weight-semibold); white-space: nowrap; cursor: pointer; }
    .queued-upload-actions .notif-row-cancel { display: inline-flex; align-items: center; justify-content: center; cursor: pointer; }
    .queued-upload-name:focus-visible, .queued-upload-open:focus-visible, .queued-upload-actions .notif-row-cancel:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }
    @media (hover: hover) { .queued-upload-open:hover { background: var(--overlay-accent-2); } }
    .mobile { padding: var(--space-3) var(--space-4); background: transparent; border-radius: 0; }
    .mobile .queued-upload-open { min-height: 36px; padding: 0 14px; font-size: 0.8125rem; }
    .mobile .queued-upload-actions .notif-row-cancel { width: 40px; height: 40px; }
</style>
