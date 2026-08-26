<script lang="ts">
    import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
    import DownloadIcon from '@lucide/svelte/icons/download';
    import ExternalLinkIcon from '@lucide/svelte/icons/external-link';
    import LoaderIcon from '@lucide/svelte/icons/loader-circle';
    import RotateCwIcon from '@lucide/svelte/icons/rotate-cw';
    import { formatBytes } from '../../utils';
    import {
        cancelUpdateDownload,
        checkForUpdates,
        downloadUpdate,
        getRestartRisks,
        installUpdate,
        openReleasePage,
    } from '../../modules/updates';
    import {
        formatPlatform,
        isVersionSkipped,
        progressPercent,
    } from './update-model';
    import {
        appVersionInfo,
        clearSkippedVersion,
        setAutoDownload,
        skipVersion,
        updatePrefs,
        updateState,
    } from './update-store';

    let confirming = $state(false);
    let risks = $state<string[]>([]);
    let preparingRestart = $state(false);

    const version = $derived($updateState.current_version || $appVersionInfo?.version || '');
    const platform = $derived(
        formatPlatform($appVersionInfo)
            .replace('macOS arm64', 'macOS · Apple silicon')
            .replace('macOS amd64', 'macOS · Intel'),
    );
    const latest = $derived($updateState.latest);
    const phase = $derived($updateState.phase);
    const percent = $derived(progressPercent($updateState.downloaded_bytes, $updateState.total_bytes));
    const skipped = $derived(
        latest ? isVersionSkipped(latest.version, $updatePrefs.skippedVersion) : false,
    );
    const lastChecked = $derived(formatChecked($updateState.checked_at));
    const checkLabel = $derived(
        phase === 'checking' ? 'Checking…' : $updateState.checked_at ? 'Check Again' : 'Check Now',
    );

    function formatChecked(ms: number): string {
        if (!ms) return 'Not checked yet';
        const date = new Date(ms);
        if (Number.isNaN(date.getTime())) return 'Not checked yet';
        return `Checked at ${date.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' })}`;
    }

    async function beginRestart(): Promise<void> {
        if (preparingRestart) return;
        preparingRestart = true;
        try {
            risks = await getRestartRisks();
        } finally {
            preparingRestart = false;
        }
        if (risks.length === 0) {
            void installUpdate();
            return;
        }
        confirming = true;
    }

    function confirmRestart(): void {
        confirming = false;
        void installUpdate();
    }

    function cancelRestart(): void {
        confirming = false;
    }

    function onSkip(): void {
        if (latest) skipVersion(latest.version);
    }
</script>

<section class="updates-panel" aria-labelledby="updates-title">
    <header class="updates-header">
        <h2 id="updates-title" tabindex="-1">Software Update</h2>
    </header>

    <div class="updates-summary">
        <div class="updates-app-name">TDrive</div>
        <div class="updates-app-meta">
            <span>{version ? `Version ${version}` : 'Version unknown'}</span>
            {#if platform}<span class="dot-sep">·</span><span>{platform}</span>{/if}
        </div>
    </div>

    <div class="updates-body">
        {#if phase === 'disabled'}
            <p class="updates-status muted" role="status" aria-live="polite">
                Automatic updates are unavailable for this development build.
            </p>
        {:else if phase === 'checking'}
            <div class="updates-status" role="status" aria-live="polite">
                <LoaderIcon class="spin" size={15} aria-hidden="true" /> Checking for updates…
            </div>
        {:else if phase === 'installing'}
            <div class="updates-status" role="status" aria-live="polite">
                <LoaderIcon class="spin" size={15} aria-hidden="true" /> Installing update…
            </div>
        {:else if phase === 'installed'}
            <div class="updates-status" role="status" aria-live="polite">
                <LoaderIcon class="spin" size={15} aria-hidden="true" /> Update installed. Restarting…
            </div>
        {:else if phase === 'downloading'}
            <div class="updates-status-row">
                <span class="updates-message-title">Downloading {latest?.version ?? ''}…</span>
                <button class="updates-link" type="button" onclick={cancelUpdateDownload}>Cancel</button>
            </div>
            <div class="updates-progress" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent}>
                <div class="updates-progress-fill" style={`width:${percent}%`}></div>
            </div>
            <div class="updates-progress-meta">
                {formatBytes($updateState.downloaded_bytes)}{#if $updateState.total_bytes > 0} / {formatBytes($updateState.total_bytes)}{/if}
                <span>{percent}%</span>
            </div>
        {:else if phase === 'ready'}
            <div class="updates-message" role="status" aria-live="polite">
                <div class="updates-message-title">Ready to install</div>
                <div class="updates-message-detail">TDrive {latest?.version ?? ''} will finish updating after a restart.</div>
            </div>
            <div class="updates-actions">
                {#if confirming}
                    <div class="updates-confirm" role="group" aria-label="Confirm restart">
                        <ul class="updates-risks">
                            {#each risks as risk (risk)}<li>{risk}</li>{/each}
                        </ul>
                        <div class="updates-confirm-buttons">
                            <button class="updates-btn primary" type="button" onclick={confirmRestart}>
                                <RotateCwIcon size={15} strokeWidth={2} aria-hidden="true" /> Restart anyway
                            </button>
                            <button class="updates-btn ghost" type="button" onclick={cancelRestart}>Cancel</button>
                        </div>
                    </div>
                {:else}
                    <button class="updates-btn primary" type="button" onclick={beginRestart} disabled={preparingRestart}>
                        <RotateCwIcon size={15} strokeWidth={2} aria-hidden="true" /> Restart to update
                    </button>
                {/if}
            </div>
            {#if latest}
                <button class="updates-link" type="button" onclick={openReleasePage}>
                    What's new <ExternalLinkIcon size={12} strokeWidth={2} aria-hidden="true" />
                </button>
            {/if}
        {:else if phase === 'available'}
            {#if $updateState.installable && !skipped}
                <div class="updates-message" role="status" aria-live="polite">
                    <div class="updates-message-title">TDrive {latest?.version ?? ''} is available.</div>
                    <div class="updates-message-detail">Download it now and restart when you're ready.</div>
                </div>
                <div class="updates-actions">
                    <button class="updates-btn primary" type="button" onclick={() => void downloadUpdate()}>
                        <DownloadIcon size={15} strokeWidth={2} aria-hidden="true" /> Download
                        {#if latest?.asset_size}<span class="updates-btn-size">{formatBytes(latest.asset_size)}</span>{/if}
                    </button>
                </div>
                <div class="updates-sublinks">
                    {#if latest}
                        <button class="updates-link" type="button" onclick={openReleasePage}>
                            What's new <ExternalLinkIcon size={12} strokeWidth={2} aria-hidden="true" />
                        </button>
                    {/if}
                    <button class="updates-link subtle" type="button" onclick={onSkip}>Skip this version</button>
                </div>
            {:else if skipped}
                <p class="updates-status muted" role="status" aria-live="polite">
                    TDrive {latest?.version ?? ''} is available, but this version is skipped.
                </p>
                <button class="updates-link" type="button" onclick={clearSkippedVersion}>Undo skip</button>
            {:else}
                <p class="updates-status muted" role="status" aria-live="polite">
                    {$updateState.install_hint || 'A newer version is available.'}
                </p>
                <button class="updates-link" type="button" onclick={openReleasePage}>
                    Get it from GitHub <ExternalLinkIcon size={12} strokeWidth={2} aria-hidden="true" />
                </button>
            {/if}
        {:else if phase === 'up_to_date'}
            <p class="updates-status" role="status" aria-live="polite">TDrive is up to date.</p>
        {:else}
            <p class="updates-status muted" role="status" aria-live="polite">
                Check for updates to keep TDrive current.
            </p>
        {/if}

        {#if $updateState.error && phase !== 'checking'}
            <div class="updates-error" role="alert">
                <CircleAlertIcon size={14} strokeWidth={2} aria-hidden="true" />
                <span>{$updateState.error}</span>
            </div>
        {/if}
    </div>

    {#if phase !== 'disabled'}
        <div class="updates-preferences">
            <div class="updates-preference">
                <div class="updates-preference-copy">
                    <div id="updates-auto-title" class="updates-preference-title">Automatic updates</div>
                    <div id="updates-auto-description" class="updates-preference-description">
                        Download updates in the background.
                    </div>
                </div>
                <button
                    class="updates-switch"
                    type="button"
                    role="switch"
                    aria-checked={$updatePrefs.autoDownload}
                    aria-labelledby="updates-auto-title"
                    aria-describedby="updates-auto-description"
                    onclick={() => setAutoDownload(!$updatePrefs.autoDownload)}
                >
                    <span class="updates-switch-thumb" aria-hidden="true"></span>
                </button>
            </div>
        </div>
        <footer class="updates-footer">
            <span class="updates-checked">{lastChecked}</span>
            <button
                class="updates-check"
                type="button"
                onclick={() => void checkForUpdates({ explicit: true })}
                disabled={phase === 'checking'}
            >
                {checkLabel}
            </button>
        </footer>
    {/if}
</section>

<style>
    .updates-panel {
        width: min(336px, calc(100vw - 44px));
        color: var(--color-text);
    }

    .updates-panel,
    .updates-panel :global(*) {
        font-family: -apple-system, BlinkMacSystemFont, 'SF Pro Text', 'Segoe UI', sans-serif;
    }

    .updates-header {
        padding: 11px 12px 12px;
        border-bottom: 1px solid var(--color-border-soft);
    }
    .updates-header h2 {
        margin: 0;
        font-size: 0.94rem;
        font-weight: 650;
        letter-spacing: 0;
        color: var(--color-text);
    }
    .updates-header h2:focus { outline: none; }

    .updates-summary {
        display: flex;
        flex-direction: column;
        gap: 3px;
        padding: 14px 12px 13px;
        border-bottom: 1px solid var(--color-border-soft);
    }
    .updates-app-name {
        font-size: 0.9rem;
        font-weight: 650;
        letter-spacing: 0;
    }
    .updates-app-meta {
        display: flex;
        flex-wrap: wrap;
        align-items: center;
        gap: 4px;
        color: var(--color-text-muted);
        font-size: 0.72rem;
        line-height: 1.35;
    }
    .dot-sep { opacity: 0.6; }

    .updates-body { padding: 13px 12px 12px; }

    .updates-status {
        display: flex;
        align-items: center;
        gap: 7px;
        margin: 0;
        color: var(--color-text-soft);
        font-size: 0.82rem;
        font-weight: 550;
        line-height: 1.38;
    }
    .updates-status.muted { color: var(--color-text-muted); font-weight: 450; }

    .updates-message { display: flex; flex-direction: column; gap: 3px; }
    .updates-message-title {
        color: var(--color-text-soft);
        font-size: 0.84rem;
        font-weight: 650;
        line-height: 1.35;
    }
    .updates-message-detail {
        color: var(--color-text-muted);
        font-size: 0.73rem;
        line-height: 1.4;
    }

    .updates-status-row {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: 12px;
        margin-bottom: 8px;
    }

    .updates-progress {
        height: 6px;
        border-radius: 999px;
        background: var(--overlay-white-2);
        overflow: hidden;
    }
    .updates-progress-fill {
        height: 100%;
        border-radius: 999px;
        background: var(--color-accent);
        transition: width var(--motion-med) var(--ease-standard);
    }
    .updates-progress-meta {
        display: flex;
        justify-content: space-between;
        margin-top: 6px;
        color: var(--color-text-muted);
        font-size: 0.74rem;
        font-variant-numeric: tabular-nums;
    }

    .updates-actions { margin: 12px 0 9px; }
    .updates-btn {
        display: inline-flex;
        align-items: center;
        justify-content: center;
        gap: 7px;
        min-height: 31px;
        padding: 7px 12px;
        border: 1px solid transparent;
        border-radius: 8px;
        font-size: 0.78rem;
        font-weight: 650;
        cursor: pointer;
        transition: background var(--motion-fast) var(--ease-standard),
            transform var(--motion-fast) var(--ease-standard),
            border-color var(--motion-fast) var(--ease-standard);
    }
    .updates-btn.primary {
        color: var(--color-on-accent);
        background: var(--color-accent);
    }
    .updates-btn.primary:hover { background: var(--color-accent-hover); }
    .updates-btn.primary:active { transform: translateY(0.5px); }
    .updates-btn.primary:disabled { opacity: 0.6; cursor: default; }
    .updates-btn.ghost {
        color: var(--color-text-muted);
        background: transparent;
        border-color: var(--color-border-soft);
    }
    .updates-btn.ghost:hover { color: var(--color-text); background: var(--overlay-white-1); }
    .updates-btn:focus-visible { outline: none; box-shadow: var(--focus-ring); }
    .updates-btn-size { opacity: 0.75; font-weight: 600; }

    .updates-sublinks {
        display: flex;
        align-items: center;
        gap: 14px;
    }
    .updates-link {
        display: inline-flex;
        align-items: center;
        gap: 5px;
        padding: 3px 0;
        color: var(--color-accent);
        background: transparent;
        border: none;
        font-size: 0.78rem;
        font-weight: 700;
        cursor: pointer;
    }
    .updates-link:hover { text-decoration: underline; }
    .updates-link:focus-visible { outline: none; box-shadow: var(--focus-ring); border-radius: 4px; }
    .updates-link.subtle { color: var(--color-text-subtle); font-weight: 600; }
    .updates-link.subtle:hover { color: var(--color-text-muted); }
    .updates-link:disabled { opacity: 0.5; cursor: default; text-decoration: none; }

    .updates-confirm {
        padding: 10px;
        background: var(--overlay-white-1);
        border: 1px solid var(--color-border-soft);
        border-radius: 10px;
    }
    .updates-risks {
        margin: 0 0 10px;
        padding-left: 18px;
        color: var(--color-text-muted);
        font-size: 0.79rem;
        line-height: 1.5;
    }
    .updates-confirm-buttons { display: flex; gap: 8px; }

    .updates-error {
        display: flex;
        align-items: flex-start;
        gap: 7px;
        margin-top: 10px;
        padding: 9px 10px;
        color: var(--color-danger);
        background: var(--overlay-danger-1);
        border-radius: 9px;
        font-size: 0.78rem;
        line-height: 1.4;
    }
    :global(.updates-error svg) { flex-shrink: 0; margin-top: 1px; }

    .updates-preferences { padding: 0 8px 8px; }
    .updates-preference {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: 14px;
        padding: 10px 10px 10px 11px;
        background: var(--overlay-white-1);
        border: 1px solid var(--color-border-soft);
        border-radius: 10px;
    }
    .updates-preference-copy { min-width: 0; }
    .updates-preference-title {
        color: var(--color-text-soft);
        font-size: 0.76rem;
        font-weight: 600;
        line-height: 1.3;
    }
    .updates-preference-description {
        margin-top: 2px;
        color: var(--color-text-muted);
        font-size: 0.68rem;
        line-height: 1.35;
    }
    .updates-switch {
        position: relative;
        flex: 0 0 auto;
        width: 36px;
        height: 22px;
        padding: 2px;
        background: var(--color-surface-3);
        border: none;
        border-radius: 999px;
        cursor: pointer;
        transition: background var(--motion-fast) var(--ease-standard);
    }
    .updates-switch[aria-checked='true'] { background: var(--color-accent); }
    .updates-switch:focus-visible { outline: none; box-shadow: var(--focus-ring); }
    .updates-switch-thumb {
        display: block;
        width: 18px;
        height: 18px;
        background: #fff;
        border-radius: 50%;
        box-shadow: 0 1px 3px rgba(0, 0, 0, 0.28);
        transform: translateX(0);
        transition: transform var(--motion-med) var(--ease-standard);
    }
    .updates-switch[aria-checked='true'] .updates-switch-thumb { transform: translateX(14px); }

    .updates-footer {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: 12px;
        padding: 0 9px 6px 12px;
    }
    .updates-checked { color: var(--color-text-subtle); font-size: 0.68rem; }
    .updates-check {
        padding: 5px 7px;
        color: var(--color-accent);
        background: transparent;
        border: none;
        border-radius: 7px;
        font-size: 0.72rem;
        font-weight: 600;
        cursor: pointer;
        transition: background var(--motion-fast) var(--ease-standard),
            color var(--motion-fast) var(--ease-standard);
    }
    .updates-check:hover:not(:disabled) { background: var(--overlay-accent-1); }
    .updates-check:focus-visible { outline: none; box-shadow: var(--focus-ring); }
    .updates-check:disabled { color: var(--color-text-subtle); cursor: default; }

    :global(.updates-panel .spin) { animation: updates-spin 0.9s linear infinite; }
    @keyframes updates-spin { to { transform: rotate(360deg); } }

    @media (prefers-reduced-motion: reduce) {
        :global(.updates-panel .spin) { animation: none; }
        .updates-progress-fill,
        .updates-switch,
        .updates-switch-thumb { transition: none; }
    }
</style>
