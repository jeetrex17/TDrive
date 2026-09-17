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
    <header class="backup-header"><div><h3>Photo &amp; video backup</h3><p>Destination: {$backupState?.destination.title || 'Current drive'}</p></div><span class:active={$backupState?.settings.enabled} class="backup-state">{$backupState?.status.phase ?? 'Loading'}</span></header>
    {#if $backupState}
        <label class="backup-switch">
            <span class="backup-copy"><strong>Back up automatically</strong><small>Back up new photos and videos from selected sources.</small></span>
            <input aria-label="Back up automatically" role="switch" type="checkbox" checked={$backupState.settings.enabled} disabled={$busy} onchange={(event) => save({ enabled: event.currentTarget.checked })} />
        </label>
        <p class="backup-access">Originals stay on your device. {#if $backupState.platform === 'android' || $backupState.platform === 'ios'}The current item may finish after you leave the app. Reopen TDrive to continue the queue.{:else}Backup runs while TDrive is open.{/if}</p>
        <div class="backup-settings" aria-label="Backup options">
            <span class="backup-group-label">Include</span>
            <div class="backup-options">
                <label class="backup-choice"><input type="checkbox" checked={$backupState.settings.photos} disabled={$busy || !$backupState.settings.enabled} onchange={(event) => save({ photos: event.currentTarget.checked })} /><span>Photos</span></label>
                <label class="backup-choice"><input type="checkbox" checked={$backupState.settings.videos} disabled={$busy || !$backupState.settings.enabled} onchange={(event) => save({ videos: event.currentTarget.checked })} /><span>Videos</span></label>
                <label class="backup-choice"><input type="checkbox" checked={$backupState.settings.futureOnly} disabled={$busy || !$backupState.settings.enabled} onchange={(event) => save({ futureOnly: event.currentTarget.checked })} /><span>New items only</span></label>
            </div>
        </div>
        <div class="backup-settings" aria-label="Backup conditions">
            <span class="backup-group-label">Only back up</span>
            <div class="backup-policy">
                <div class="backup-policy-option">
                    <label class="backup-choice" title={$backupState.capabilities.wifiOnly.detail}><input type="checkbox" checked={$backupState.settings.wifiOnly} disabled={$busy || !$backupState.settings.enabled || !$backupState.capabilities.wifiOnly.supported} onchange={(event) => save({ wifiOnly: event.currentTarget.checked })} /><span>Wi-Fi only</span></label>
                    {#if !$backupState.capabilities.wifiOnly.supported}<small class="backup-option-note">{$backupState.capabilities.wifiOnly.detail || 'Unavailable on this device.'}</small>{/if}
                </div>
                <div class="backup-policy-option">
                    <label class="backup-choice" title={$backupState.capabilities.chargingOnly.detail}><input type="checkbox" checked={$backupState.settings.chargingOnly} disabled={$busy || !$backupState.settings.enabled || !$backupState.capabilities.chargingOnly.supported} onchange={(event) => save({ chargingOnly: event.currentTarget.checked })} /><span>While charging</span></label>
                    {#if !$backupState.capabilities.chargingOnly.supported}<small class="backup-option-note">{$backupState.capabilities.chargingOnly.detail || 'Unavailable on this device.'}</small>{/if}
                </div>
            </div>
        </div>
        {#if $backupState.capabilities.access.status}<p class="backup-access">Library access: {$backupState.capabilities.access.status}{$backupState.capabilities.access.detail ? ` · ${$backupState.capabilities.access.detail}` : ''}</p>{/if}
        <div class="backup-sources"><div class="backup-section-heading"><strong>Sources</strong>{#if $backupState.platform !== 'android' && $backupState.platform !== 'ios'}<button class="secondary-btn backup-add-source" type="button" disabled={$busy} onclick={() => void choosePhotoBackupFolder()}><FolderPlusIcon size={15} /> Add folder</button>{:else}<button class="secondary-btn backup-add-source" type="button" disabled={$busy} onclick={() => void loadPhotoBackupCandidates()}>Choose sources</button>{/if}</div>
            {#each $backupState.sources as source (source.id)}<div class="backup-source"><span>{source.name}</span><button type="button" aria-label={`Remove ${source.name}`} disabled={$busy} onclick={() => void deletePhotoBackupSource(source.id)}><Trash2Icon size={15} /></button></div>{:else}<p class="backup-source-empty">Choose a folder or library to start backing up.</p>{/each}
            {#each $candidates.filter((candidate) => !$backupState.sources.some((source) => source.id === candidate.id)) as candidate (candidate.id)}<button class="backup-candidate" type="button" disabled={$busy} onclick={() => void selectPhotoBackupSource(candidate)}>Add {candidate.name}</button>{/each}
        </div>
        <div class="backup-progress"><div><strong>{$backupState.status.pending + $backupState.status.uploading} waiting</strong><span>{$backupState.status.complete} completed{$backupState.status.failed ? ` · ${$backupState.status.failed} failed` : ''}{$backupState.status.paused ? ` · ${$backupState.status.paused} interrupted` : ''}</span></div>{#if $backupState.status.bytesTotal > 0}<progress value={$backupState.status.bytesDone} max={$backupState.status.bytesTotal}></progress><small>{formatBytes($backupState.status.bytesDone)} of {formatBytes($backupState.status.bytesTotal)}</small>{/if}{#if $backupState.status.message}<small>{$backupState.status.message}</small>{/if}</div>
        <div class="backup-actions">{#if $backupState.status.phase === 'uploading' || $backupState.status.phase === 'scanning' || $backupState.status.phase === 'queued'}<button class="secondary-btn" type="button" disabled={$busy} onclick={() => void pausePhotoBackupNow()}><PauseIcon size={15} /> Pause</button>{:else if $backupState.manualPaused}<button class="primary-btn" type="button" disabled={$busy} onclick={() => void resumePhotoBackupNow()}><PlayIcon size={15} /> Resume</button>{:else}<button class="primary-btn" type="button" disabled={$busy || !$backupState.settings.enabled || (!$backupState.settings.photos && !$backupState.settings.videos) || !$backupState.sources.some((source) => source.enabled)} onclick={() => void startPhotoBackup()}><PlayIcon size={15} /> Back up now</button>{/if}{#if $backupState.status.failed || $backupState.status.paused > 0}<button class="secondary-btn" type="button" disabled={$busy || $backupState.manualPaused} title={$backupState.status.paused ? 'The prior upload outcome is unknown. Retrying may create a duplicate.' : ''} onclick={() => void retryPhotoBackupNow()}><RotateCwIcon size={15} /> Retry interrupted</button>{/if}</div>
    {:else}<p>Loading backup settings…</p>{/if}
    {#if $error}<p class="backup-error" role="alert">{$error}</p>{/if}
</section>

<style>
    .photo-backup-panel { box-sizing: border-box; padding: 14px 16px 16px; display: grid; gap: 13px; border-top: 1px solid var(--border); }
    .backup-header, .backup-switch, .backup-source, .backup-section-heading, .backup-actions, .backup-progress > div { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
    .backup-header > div, .backup-copy, .backup-source > span { min-width: 0; }
    h3, p { margin: 0; } h3 { font-size: .9rem; } p, small { color: var(--text-secondary); font-size: 12px; line-height: 1.4; }
    .backup-state { flex: 0 0 auto; color: var(--text-muted); font-size: 12px; text-transform: capitalize; } .backup-state.active { color: var(--accent); }
    .backup-switch { min-height: 48px; padding: 10px 12px; border: 1px solid var(--border); border-radius: var(--radius-md); background: var(--overlay-white-1); cursor: pointer; }
    .backup-switch strong, .backup-switch small { display: block; } .backup-switch strong { color: var(--text-main); font-size: 13px; } .backup-switch small { margin-top: 2px; }
    .backup-switch input { appearance: none; box-sizing: border-box; position: relative; flex: 0 0 auto; inline-size: 36px; min-inline-size: 36px; block-size: 20px; min-block-size: 20px; width: 36px; height: 20px; padding: 0; margin: 0 0 0 0; border: 1px solid var(--border); border-radius: var(--radius-pill); background: var(--bg-panel); cursor: pointer; transition: background-color var(--motion-fast) var(--ease-standard), border-color var(--motion-fast) var(--ease-standard); }
    .backup-switch input::after { content: ''; position: absolute; top: 2px; left: 2px; inline-size: 14px; block-size: 14px; border-radius: 50%; background: var(--text-muted); transition: transform var(--motion-fast) var(--ease-standard), background-color var(--motion-fast) var(--ease-standard); }
    .backup-switch input:checked { background: var(--accent); border-color: var(--accent); } .backup-switch input:checked::after { transform: translateX(16px); background: var(--color-on-accent); }
    .backup-switch input:disabled, .backup-choice input:disabled { cursor: not-allowed; opacity: .52; } .backup-switch:has(input:disabled) { cursor: not-allowed; }
    .backup-switch input:focus-visible, .backup-choice input:focus-visible, .backup-source button:focus-visible, .backup-candidate:focus-visible { outline: none; box-shadow: var(--focus-ring); }
    .backup-access { padding-inline: 1px; }
    .backup-settings { display: grid; gap: 7px; } .backup-group-label { color: var(--text-muted); font-size: 11px; font-weight: 700; letter-spacing: .02em; text-transform: uppercase; }
    .backup-options, .backup-policy { display: flex; flex-wrap: wrap; gap: 6px 12px; }
    .backup-choice { display: inline-flex; align-items: center; gap: 7px; min-block-size: 28px; color: var(--text-main); font-size: 12px; cursor: pointer; } .backup-choice input { box-sizing: border-box; flex: 0 0 auto; inline-size: 15px; min-inline-size: 15px; block-size: 15px; min-block-size: 15px; width: 15px; height: 15px; padding: 0; margin: 0; border-radius: 3px; font-size: initial; accent-color: var(--accent); }
    .backup-choice:has(input:disabled) { color: var(--text-muted); cursor: not-allowed; }
    .backup-policy-option { display: grid; gap: 2px; } .backup-option-note { max-width: 220px; color: var(--text-muted); }
    .backup-sources { display: grid; gap: 7px; } .backup-section-heading strong { color: var(--text-main); font-size: 13px; } .backup-add-source { width: auto; min-height: 32px; padding: 6px 9px; font-size: 12px; }
    .backup-source { border: 1px solid var(--border); border-radius: var(--radius-md); padding: 7px 8px 7px 10px; color: var(--text-main); font-size: 12px; } .backup-source > span { overflow-wrap: anywhere; } .backup-source button { display: grid; place-items: center; flex: 0 0 auto; background: transparent; color: var(--text-muted); border: 0; border-radius: var(--radius-sm); padding: 4px; cursor: pointer; } .backup-source button:hover { color: var(--danger, #d84b4b); background: var(--overlay-white-2); }
    .backup-source-empty { padding: 2px 1px; } .backup-candidate { background: transparent; border: 0; color: var(--accent); min-height: 32px; padding: 4px 0; text-align: left; font-size: 12px; cursor: pointer; }
    .backup-progress { display: grid; gap: 5px; } .backup-progress strong { color: var(--text-main); font-size: 12px; } .backup-progress span { color: var(--text-muted); font-size: 12px; } progress { width: 100%; accent-color: var(--accent); } .backup-actions { justify-content: flex-start; flex-wrap: wrap; } .backup-actions button { width: auto; min-height: 34px; padding: 7px 10px; display: inline-flex; align-items: center; gap: 6px; font-size: 12px; } .backup-actions button:disabled { cursor: not-allowed; opacity: .48; }
    .backup-error { color: var(--danger, #d84b4b); }
    :global(html.mobile) .backup-choice, :global(html.mobile) .backup-candidate, :global(html.mobile) .backup-add-source, :global(html.mobile) .backup-actions button { min-block-size: 44px; }
    @media (max-width: 380px) { .photo-backup-panel { padding-inline: 12px; } .backup-switch { align-items: flex-start; } .backup-options, .backup-policy { display: grid; gap: 4px; } .backup-section-heading { align-items: flex-start; } }
</style>
