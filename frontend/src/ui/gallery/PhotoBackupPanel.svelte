<script lang="ts">
    import { onMount } from 'svelte';
    import FolderPlusIcon from '@lucide/svelte/icons/folder-plus';
    import PauseIcon from '@lucide/svelte/icons/pause';
    import PlayIcon from '@lucide/svelte/icons/play';
    import RotateCwIcon from '@lucide/svelte/icons/rotate-cw';
    import Trash2Icon from '@lucide/svelte/icons/trash-2';
    import { formatBytes } from '../../utils';
    import { choosePhotoBackupFolder, deletePhotoBackupSource, loadPhotoBackupCandidates, pausePhotoBackupNow, photoBackupBusy, photoBackupCandidates, photoBackupError, photoBackupState, refreshPhotoBackup, resumePhotoBackupNow, retryPhotoBackupNow, selectPhotoBackupSource, startPhotoBackup, updatePhotoBackupSettings } from '../../modules/photo-backup/controller';
    import type { PhotoBackupSettings } from '../../api/photo-backup';

    onMount(() => { void refreshPhotoBackup(); });
    const backupState = photoBackupState;
    const candidates = photoBackupCandidates;
    const busy = photoBackupBusy;
    const error = photoBackupError;
    function save(change: Partial<PhotoBackupSettings>): void { if (!$backupState) return; void updatePhotoBackupSettings({ ...$backupState.settings, ...change }); }
</script>

<section class="photo-backup-panel" aria-label="Photo and video backup" aria-busy={$busy}>
    <header><div><h3>Photo &amp; video backup</h3><p>Destination: {$backupState?.destination.title || 'Current drive'}</p></div><span class:active={$backupState?.settings.enabled} class="backup-state">{$backupState?.status.phase ?? 'Loading'}</span></header>
    {#if $backupState}
        <label class="backup-switch"><span><strong>Back up automatically</strong><small>Back up new photos and videos from selected sources.</small></span><input aria-label="Back up automatically" type="checkbox" checked={$backupState.settings.enabled} disabled={$busy} onchange={(event) => save({ enabled: event.currentTarget.checked })} /></label>
        <p class="backup-access">Originals stay on your device. {#if $backupState.platform === 'android' || $backupState.platform === 'ios'}The current item may finish after you leave the app. Reopen TDrive to continue the queue.{:else}Backup runs while TDrive is open.{/if}</p>
        <div class="backup-options">
            <label><input type="checkbox" checked={$backupState.settings.photos} disabled={$busy || !$backupState.settings.enabled} onchange={(event) => save({ photos: event.currentTarget.checked })} /> Photos</label>
            <label><input type="checkbox" checked={$backupState.settings.videos} disabled={$busy || !$backupState.settings.enabled} onchange={(event) => save({ videos: event.currentTarget.checked })} /> Videos</label>
            <label><input type="checkbox" checked={$backupState.settings.futureOnly} disabled={$busy || !$backupState.settings.enabled} onchange={(event) => save({ futureOnly: event.currentTarget.checked })} /> New items only</label>
        </div>
        <div class="backup-policy">
            <label title={$backupState.capabilities.wifiOnly.detail}><input type="checkbox" checked={$backupState.settings.wifiOnly} disabled={$busy || !$backupState.settings.enabled || !$backupState.capabilities.wifiOnly.supported} onchange={(event) => save({ wifiOnly: event.currentTarget.checked })} /> Wi-Fi only</label>
            {#if !$backupState.capabilities.wifiOnly.supported}<small>{$backupState.capabilities.wifiOnly.label}</small>{/if}
            <label title={$backupState.capabilities.chargingOnly.detail}><input type="checkbox" checked={$backupState.settings.chargingOnly} disabled={$busy || !$backupState.settings.enabled || !$backupState.capabilities.chargingOnly.supported} onchange={(event) => save({ chargingOnly: event.currentTarget.checked })} /> While charging</label>
            {#if !$backupState.capabilities.chargingOnly.supported}<small>{$backupState.capabilities.chargingOnly.label}</small>{/if}
        </div>
        {#if $backupState.capabilities.access.status}<p class="backup-access">Library access: {$backupState.capabilities.access.status}{$backupState.capabilities.access.detail ? ` · ${$backupState.capabilities.access.detail}` : ''}</p>{/if}
        <div class="backup-sources"><div class="backup-section-heading"><strong>Sources</strong>{#if $backupState.platform !== 'android' && $backupState.platform !== 'ios'}<button class="secondary-btn" type="button" disabled={$busy} onclick={() => void choosePhotoBackupFolder()}><FolderPlusIcon size={15} /> Add folder</button>{:else}<button class="secondary-btn" type="button" disabled={$busy} onclick={() => void loadPhotoBackupCandidates()}>Choose sources</button>{/if}</div>
            {#each $backupState.sources as source (source.id)}<div class="backup-source"><span>{source.name}</span><button type="button" aria-label={`Remove ${source.name}`} disabled={$busy} onclick={() => void deletePhotoBackupSource(source.id)}><Trash2Icon size={15} /></button></div>{:else}<p>No source has been selected.</p>{/each}
            {#each $candidates.filter((candidate) => !$backupState.sources.some((source) => source.id === candidate.id)) as candidate (candidate.id)}<button class="backup-candidate" type="button" disabled={$busy} onclick={() => void selectPhotoBackupSource(candidate)}>Add {candidate.name}</button>{/each}
        </div>
        <div class="backup-progress"><div><strong>{$backupState.status.pending + $backupState.status.uploading} waiting</strong><span>{$backupState.status.complete} completed{$backupState.status.failed ? ` · ${$backupState.status.failed} failed` : ''}{$backupState.status.paused ? ` · ${$backupState.status.paused} interrupted` : ''}</span></div>{#if $backupState.status.bytesTotal > 0}<progress value={$backupState.status.bytesDone} max={$backupState.status.bytesTotal}></progress><small>{formatBytes($backupState.status.bytesDone)} of {formatBytes($backupState.status.bytesTotal)}</small>{/if}{#if $backupState.status.message}<small>{$backupState.status.message}</small>{/if}</div>
        <div class="backup-actions">{#if $backupState.status.phase === 'uploading' || $backupState.status.phase === 'scanning' || $backupState.status.phase === 'queued'}<button class="secondary-btn" type="button" disabled={$busy} onclick={() => void pausePhotoBackupNow()}><PauseIcon size={15} /> Pause</button>{:else if $backupState.manualPaused}<button class="primary-btn" type="button" disabled={$busy} onclick={() => void resumePhotoBackupNow()}><PlayIcon size={15} /> Resume</button>{:else}<button class="primary-btn" type="button" disabled={$busy || !$backupState.settings.enabled || (!$backupState.settings.photos && !$backupState.settings.videos)} onclick={() => void startPhotoBackup()}><PlayIcon size={15} /> Back up now</button>{/if}{#if $backupState.status.failed || $backupState.status.paused > 0}<button class="secondary-btn" type="button" disabled={$busy || $backupState.manualPaused} title={$backupState.status.paused ? 'The prior upload outcome is unknown. Retrying may create a duplicate.' : ''} onclick={() => void retryPhotoBackupNow()}><RotateCwIcon size={15} /> Retry interrupted</button>{/if}</div>
    {:else}<p>Loading backup settings…</p>{/if}
    {#if $error}<p class="backup-error" role="alert">{$error}</p>{/if}
</section>

<style>
    .photo-backup-panel { padding: 16px; display: grid; gap: 14px; border-top: 1px solid var(--border); }
    header, .backup-switch, .backup-source, .backup-section-heading, .backup-actions, .backup-progress > div { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
    h3, p { margin: 0; } h3 { font-size: .9rem; } p, small { color: var(--text-secondary); font-size: 12px; line-height: 1.4; }
    .backup-state { color: var(--text-muted); font-size: 12px; text-transform: capitalize; } .backup-state.active { color: var(--accent); }
    .backup-switch strong, .backup-switch small { display: block; } .backup-switch input { inline-size: 38px; }
    .backup-options { display: flex; flex-wrap: wrap; gap: 8px 14px; font-size: 12px; }
    .backup-policy { display: grid; gap: 5px; font-size: 12px; } .backup-policy small { margin-left: 22px; }
    .backup-sources { display: grid; gap: 7px; } .backup-source { border: 1px solid var(--border); border-radius: var(--radius-md); padding: 7px 8px 7px 10px; font-size: 12px; } .backup-source button { background: transparent; color: var(--text-muted); border: 0; padding: 4px; cursor: pointer; }
    .backup-candidate { background: transparent; border: 0; color: var(--accent); min-height: 44px; padding: 5px 0; text-align: left; font-size: 12px; cursor: pointer; }
    .backup-progress { display: grid; gap: 5px; } .backup-progress span { color: var(--text-muted); font-size: 12px; } progress { width: 100%; accent-color: var(--accent); } .backup-actions { justify-content: flex-start; flex-wrap: wrap; } .backup-actions button { min-height: 36px; display: inline-flex; align-items: center; gap: 6px; }
    .backup-error { color: var(--danger, #d84b4b); }
</style>
