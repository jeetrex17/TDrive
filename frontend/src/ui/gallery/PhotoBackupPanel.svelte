<script lang="ts">
    /**
     * Photo & video backup, as one card. It reads as three things top to bottom:
     * the switch that turns it on, one situation block that says what is
     * happening and offers the one thing to do about it, and the choices
     * underneath. The situation is computed once in photo-backup-view.ts; this
     * file only renders it. Backend refusals arrive through the controller in
     * the backend's own words, never as a wrapped call error.
     */
    import { onMount } from 'svelte';
    import CheckIcon from '@lucide/svelte/icons/check';
    import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
    import CloudUploadIcon from '@lucide/svelte/icons/cloud-upload';
    import FolderPlusIcon from '@lucide/svelte/icons/folder-plus';
    import ImagesIcon from '@lucide/svelte/icons/images';
    import LockKeyholeIcon from '@lucide/svelte/icons/lock-keyhole';
    import PauseIcon from '@lucide/svelte/icons/pause';
    import PlayIcon from '@lucide/svelte/icons/play';
    import PlusIcon from '@lucide/svelte/icons/plus';
    import RotateCwIcon from '@lucide/svelte/icons/rotate-cw';
    import Trash2Icon from '@lucide/svelte/icons/trash-2';
    import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';
    import Button from '../Button.svelte';
    import IconButton from '../IconButton.svelte';
    import ProgressBar from '../ProgressBar.svelte';
    import SwitchRow from '../SwitchRow.svelte';
    import { formatBytes } from '../../utils';
    import { futureOnlyDescription, type PhotoBackupSettings } from '../../api/photo-backup';
    import {
        choosePhotoBackupFolder, deletePhotoBackupSource, loadPhotoBackupCandidates, pausePhotoBackupNow,
        photoBackupBusy, photoBackupCandidates, photoBackupError, photoBackupState, refreshPhotoBackup,
        resumePhotoBackupNow, retryPhotoBackupNow, selectPhotoBackupSource, startPhotoBackup, updatePhotoBackupSettings,
    } from '../../modules/photo-backup/controller';
    import { actionLabel, canStart, describeBackup, summaryLine, type BackupAction } from './photo-backup-view';

    onMount(() => { void refreshPhotoBackup(); });

    const backupState = photoBackupState;
    const candidates = photoBackupCandidates;
    const busy = photoBackupBusy;
    const error = photoBackupError;

    const situation = $derived($backupState ? describeBackup($backupState) : null);
    const summary = $derived($backupState ? summaryLine($backupState.status) : '');
    const startable = $derived($backupState ? canStart($backupState) : false);
    const phone = $derived($backupState?.platform === 'android' || $backupState?.platform === 'ios');
    const unselected = $derived($candidates.filter((candidate) => !$backupState?.sources.some((source) => source.id === candidate.id)));
    // Only a restriction is worth a line; full access is the expected case.
    const accessNote = $derived(($backupState && !['', 'available', 'granted'].includes($backupState.capabilities.access.status)) ? $backupState.capabilities.access.detail : '');

    const handlers: Record<BackupAction, () => Promise<void>> = {
        start: startPhotoBackup, pause: pausePhotoBackupNow, resume: resumePhotoBackupNow, retry: retryPhotoBackupNow,
    };

    function save(change: Partial<PhotoBackupSettings>): void {
        if (!$backupState) return;
        void updatePhotoBackupSettings({ ...$backupState.settings, ...change });
    }

    function disabledFor(action: BackupAction): boolean {
        return $busy || (action === 'start' && !startable);
    }
</script>

<section class="photo-backup" aria-label="Photo and video backup" aria-busy={$busy}>
    {#if $backupState}
        {@const state = $backupState}
        <div class="pb-master">
            <SwitchRow
                title="Back up photos & videos"
                description={state.settings.enabled ? `To ${state.destination.title || 'this drive'}` : 'Keep a copy of new photos and videos in this drive.'}
                checked={state.settings.enabled}
                disabled={$busy}
                onchange={(enabled) => save({ enabled })}
            />
        </div>

        {#if state.settings.enabled && situation}
            <div class="pb-situation" data-tone={situation.tone} role="status" aria-live="polite">
                <div class="pb-situation-icon" aria-hidden="true">
                    {#if situation.tone === 'busy'}
                        <span class="pb-spinner"></span>
                    {:else if situation.tone === 'locked'}
                        <LockKeyholeIcon size={16} strokeWidth={2} />
                    {:else if situation.tone === 'danger'}
                        <CircleAlertIcon size={16} strokeWidth={2} />
                    {:else if situation.tone === 'warning'}
                        <TriangleAlertIcon size={16} strokeWidth={2} />
                    {:else if situation.tone === 'success'}
                        <CheckIcon size={16} strokeWidth={2.4} />
                    {:else}
                        <CloudUploadIcon size={16} strokeWidth={2} />
                    {/if}
                </div>
                <div class="pb-situation-copy">
                    <div class="pb-situation-title">{situation.title}</div>
                    {#if situation.body}
                        <div class="pb-situation-body" class:is-file={situation.tone === 'busy' && Boolean(state.status.currentFile)}>{situation.body}</div>
                    {/if}
                    {#if situation.progress}
                        <div class="pb-progress">
                            {#if state.status.currentFileBytesTotal > 0}
                                <ProgressBar value={state.status.currentFileBytesDone} max={state.status.currentFileBytesTotal} label={`${formatBytes(state.status.currentFileBytesDone)} of ${formatBytes(state.status.currentFileBytesTotal)}`} showValue />
                            {:else}
                                <ProgressBar indeterminate />
                            {/if}
                        </div>
                    {/if}
                    {#if summary}
                        <div class="pb-summary">{summary}</div>
                    {/if}
                    {#if situation.primary || situation.secondary}
                        <div class="pb-actions">
                            {#if situation.primary}
                                {@const primary = situation.primary}
                                <Button size="sm" disabled={disabledFor(primary)} onclick={() => void handlers[primary]()}>
                                    {#if primary === 'retry'}<RotateCwIcon size={14} strokeWidth={2.2} aria-hidden="true" />{:else}<PlayIcon size={14} strokeWidth={2.2} aria-hidden="true" />{/if}
                                    {actionLabel(primary, state.encryptionRequired)}
                                </Button>
                            {/if}
                            {#if situation.secondary}
                                {@const secondary = situation.secondary}
                                <Button variant="secondary" size="sm" disabled={disabledFor(secondary)} onclick={() => void handlers[secondary]()}>
                                    <PauseIcon size={14} strokeWidth={2.2} aria-hidden="true" />
                                    {actionLabel(secondary, false)}
                                </Button>
                            {/if}
                        </div>
                    {/if}
                </div>
            </div>

            <div class="pb-group" aria-label="Sources">
                <div class="pb-group-head">
                    <span class="pb-group-label">Backing up</span>
                    {#if phone}
                        <Button variant="secondary" size="sm" disabled={$busy} onclick={() => void loadPhotoBackupCandidates()}>
                            <PlusIcon size={14} strokeWidth={2.2} aria-hidden="true" />
                            Choose sources
                        </Button>
                    {:else}
                        <Button variant="secondary" size="sm" disabled={$busy} onclick={() => void choosePhotoBackupFolder()}>
                            <FolderPlusIcon size={14} strokeWidth={2.2} aria-hidden="true" />
                            Add folder
                        </Button>
                    {/if}
                </div>
                <ul class="pb-sources" role="list">
                    {#each state.sources as source (source.id)}
                        <li class="pb-source">
                            <ImagesIcon class="pb-source-icon" size={16} strokeWidth={1.9} aria-hidden="true" />
                            <span class="pb-source-name">{source.name}</span>
                            <IconButton label={`Remove ${source.name}`} size="sm" disabled={$busy} onclick={() => void deletePhotoBackupSource(source.id)}>
                                <Trash2Icon size={15} strokeWidth={2} />
                            </IconButton>
                        </li>
                    {:else}
                        <li class="pb-source pb-source-empty">{phone ? 'Choose an album or your whole library.' : 'Add a folder to watch for new photos and videos.'}</li>
                    {/each}
                    {#each unselected as candidate (candidate.id)}
                        <li>
                            <button class="pb-candidate" type="button" disabled={$busy} onclick={() => void selectPhotoBackupSource(candidate)}>
                                <PlusIcon size={15} strokeWidth={2.2} aria-hidden="true" />
                                <span>Add {candidate.name}</span>
                            </button>
                        </li>
                    {/each}
                </ul>
                {#if accessNote}
                    <p class="pb-note">{accessNote}</p>
                {/if}
            </div>

            <div class="pb-group" aria-label="Backup options">
                <span class="pb-group-label">Include</span>
                <SwitchRow title="Photos" checked={state.settings.photos} disabled={$busy} onchange={(photos) => save({ photos })} />
                <SwitchRow title="Videos" checked={state.settings.videos} disabled={$busy} onchange={(videos) => save({ videos })} />
                <SwitchRow title="New items only" description={futureOnlyDescription} checked={state.settings.futureOnly} disabled={$busy} onchange={(futureOnly) => save({ futureOnly })} />
                {#if state.capabilities.wifiOnly.supported}
                    <SwitchRow title="Wi-Fi only" description="Never uses mobile data." checked={state.settings.wifiOnly} disabled={$busy} onchange={(wifiOnly) => save({ wifiOnly })} />
                {/if}
            </div>
        {/if}
    {:else}
        <p class="pb-loading" role="status">Loading backup settings…</p>
    {/if}

    {#if $error}
        <p class="pb-error" role="alert">
            <CircleAlertIcon size={14} strokeWidth={2} aria-hidden="true" />
            <span>{$error}</span>
        </p>
    {/if}
</section>

<style>
    .photo-backup {
        box-sizing: border-box;
        display: grid;
        gap: var(--space-4);
        padding: var(--space-3) var(--space-3) var(--space-4);
        color: var(--text-main);
    }

    .pb-master {
        padding-bottom: var(--space-2);
        border-bottom: 1px solid var(--border);
    }

    /* The situation block. Its tone colours one ring and one glyph; the copy
       stays neutral so a warning never shouts. */
    .pb-situation {
        display: grid;
        grid-template-columns: auto minmax(0, 1fr);
        gap: var(--space-3);
        align-items: start;
    }

    .pb-situation-icon {
        width: 32px;
        height: 32px;
        display: grid;
        place-items: center;
        border: 1px solid var(--border);
        border-radius: var(--radius-full);
        color: var(--text-muted);
        background: var(--surface-control);
    }

    .pb-situation[data-tone='busy'] .pb-situation-icon,
    .pb-situation[data-tone='locked'] .pb-situation-icon {
        color: var(--accent);
        border-color: color-mix(in srgb, var(--accent) 42%, transparent);
        background: color-mix(in srgb, var(--accent) 12%, transparent);
    }

    .pb-situation[data-tone='success'] .pb-situation-icon {
        color: var(--success);
        border-color: color-mix(in srgb, var(--success) 42%, transparent);
        background: color-mix(in srgb, var(--success) 12%, transparent);
    }

    .pb-situation[data-tone='warning'] .pb-situation-icon {
        color: var(--color-warning);
        border-color: color-mix(in srgb, var(--color-warning) 42%, transparent);
        background: color-mix(in srgb, var(--color-warning) 12%, transparent);
    }

    .pb-situation[data-tone='danger'] .pb-situation-icon {
        color: var(--danger);
        border-color: color-mix(in srgb, var(--danger) 42%, transparent);
        background: color-mix(in srgb, var(--danger) 12%, transparent);
    }

    .pb-spinner {
        width: 14px;
        height: 14px;
        border-radius: var(--radius-full);
        border: 2px solid color-mix(in srgb, var(--accent) 30%, transparent);
        border-top-color: var(--accent);
        animation: pb-spin 720ms linear infinite;
    }

    @keyframes pb-spin {
        to { transform: rotate(360deg); }
    }

    .pb-situation-copy {
        min-width: 0;
        display: grid;
        gap: var(--space-1);
        padding-top: 5px;
    }

    .pb-situation-title {
        font-size: var(--font-size-sm);
        font-weight: 800;
        line-height: 1.35;
    }

    .pb-situation-body {
        color: var(--text-muted);
        font-size: var(--font-size-xs);
        line-height: 1.45;
    }

    /* A file name is one line and gets cut from the end, where the counter is. */
    .pb-situation-body.is-file {
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
        font-variant-numeric: tabular-nums;
    }

    .pb-progress {
        margin-top: var(--space-1);
    }

    .pb-summary {
        color: var(--text-muted);
        font-size: var(--font-size-xs);
        font-variant-numeric: tabular-nums;
    }

    .pb-actions {
        display: flex;
        flex-wrap: wrap;
        gap: var(--space-2);
        margin-top: var(--space-2);
    }

    .pb-group {
        display: grid;
        gap: var(--space-1);
        padding-top: var(--space-3);
        border-top: 1px solid var(--border);
    }

    .pb-group-head {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: var(--space-3);
        min-height: 32px;
    }

    .pb-group-label {
        color: var(--text-muted);
        font-size: 11px;
        font-weight: var(--weight-strong);
        letter-spacing: 0.04em;
        text-transform: uppercase;
    }

    .pb-sources {
        margin: 0;
        padding: 0;
        list-style: none;
        display: grid;
    }

    .pb-source {
        display: flex;
        align-items: center;
        gap: var(--space-3);
        min-height: 36px;
        font-size: var(--font-size-sm);
    }

    .pb-source + .pb-source {
        border-top: 1px solid var(--border);
    }

    .pb-source :global(.pb-source-icon) {
        flex: 0 0 auto;
        color: var(--text-muted);
    }

    .pb-source-name {
        flex: 1 1 auto;
        min-width: 0;
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
    }

    .pb-source-empty {
        color: var(--text-muted);
        font-size: var(--font-size-xs);
    }

    .pb-candidate {
        display: inline-flex;
        align-items: center;
        gap: var(--space-2);
        min-height: 36px;
        padding: 0;
        border: 0;
        background: transparent;
        color: var(--accent);
        font: inherit;
        font-size: var(--font-size-sm);
        font-weight: var(--weight-semibold);
        cursor: pointer;
    }

    .pb-candidate:disabled {
        cursor: not-allowed;
        opacity: 0.55;
    }

    .pb-candidate:focus-visible {
        outline: none;
        border-radius: var(--radius-sm);
        box-shadow: var(--focus-ring);
    }

    .pb-note,
    .pb-loading {
        margin: 0;
        color: var(--text-muted);
        font-size: var(--font-size-xs);
        line-height: 1.45;
    }

    .pb-error {
        display: flex;
        align-items: flex-start;
        gap: var(--space-2);
        margin: 0;
        color: var(--danger);
        font-size: var(--font-size-xs);
        line-height: 1.45;
    }

    .pb-error :global(svg) {
        flex: 0 0 auto;
        margin-top: 2px;
    }

    @media (prefers-reduced-motion: reduce) {
        .pb-spinner {
            animation: none;
        }
    }

    /* Phone: the card's own rhythm -- 14px gutters, 52px rows, the mobile type
       scale -- and every control a full-height tap target. */
    :global(html.mobile) .photo-backup {
        gap: var(--space-3);
        padding: 0 14px 14px;
        border-top: 1px solid var(--color-border-soft);
    }

    :global(html.mobile) .pb-master {
        padding-bottom: 0;
        border-bottom: 0;
    }

    :global(html.mobile) .pb-situation {
        padding: var(--space-3);
        border-radius: var(--radius-lg);
        background: var(--overlay-neutral-2);
    }

    :global(html.mobile) .pb-situation-title {
        font-size: var(--mobile-type-body);
    }

    :global(html.mobile) .pb-situation-body,
    :global(html.mobile) .pb-summary,
    :global(html.mobile) .pb-note,
    :global(html.mobile) .pb-source-empty {
        font-size: var(--mobile-type-meta);
    }

    :global(html.mobile) .pb-source {
        min-height: 48px;
        font-size: var(--mobile-type-body);
    }

    :global(html.mobile) .pb-candidate {
        min-height: 48px;
        font-size: var(--mobile-type-body);
    }

    :global(html.mobile) .pb-actions :global(.ui-button),
    :global(html.mobile) .pb-group-head :global(.ui-button) {
        min-height: 44px;
        padding: 0 var(--space-4);
        font-size: var(--mobile-type-meta);
    }

    :global(html.mobile) .pb-source :global(.ui-icon-button) {
        width: 44px;
        height: 44px;
    }
</style>
