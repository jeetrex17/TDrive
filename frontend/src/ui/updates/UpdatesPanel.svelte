<script lang="ts">
    import CheckIcon from '@lucide/svelte/icons/check';
    import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
    import CopyIcon from '@lucide/svelte/icons/copy';
    import DownloadIcon from '@lucide/svelte/icons/download';
    import ExternalLinkIcon from '@lucide/svelte/icons/external-link';
    import LoaderIcon from '@lucide/svelte/icons/loader-circle';
    import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';
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

    let copied = $state(false);
    let confirming = $state(false);
    let risks = $state<string[]>([]);
    let preparingRestart = $state(false);

    const version = $derived($updateState.current_version || $appVersionInfo?.version || '');
    const platform = $derived(formatPlatform($appVersionInfo));
    const latest = $derived($updateState.latest);
    const phase = $derived($updateState.phase);
    const percent = $derived(progressPercent($updateState.downloaded_bytes, $updateState.total_bytes));
    const skipped = $derived(
        latest ? isVersionSkipped(latest.version, $updatePrefs.skippedVersion) : false,
    );
    const lastChecked = $derived(formatChecked($updateState.checked_at));

    function formatChecked(ms: number): string {
        if (!ms) return 'Not checked yet';
        const date = new Date(ms);
        if (Number.isNaN(date.getTime())) return 'Not checked yet';
        return `Last checked ${date.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' })}`;
    }

    async function copyBuildInfo(): Promise<void> {
        const info = $appVersionInfo;
        const text = `TDrive ${version}${info ? ` · ${info.os} ${info.arch}` : ''}`;
        try {
            await navigator.clipboard?.writeText(text);
            copied = true;
            setTimeout(() => (copied = false), 1600);
        } catch {
            // Clipboard can be blocked in the webview; failing quietly is fine.
        }
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
        <h2 id="updates-title">Software update</h2>
    </header>

    <div class="updates-identity">
        <div class="updates-app-name">TDrive</div>
        <div class="updates-app-meta">
            <span>{version ? `Version ${version}` : 'Version unknown'}</span>
            {#if platform}<span class="dot-sep">·</span><span>{platform}</span>{/if}
        </div>
        <button
            class="updates-copy"
            type="button"
            onclick={copyBuildInfo}
            aria-label="Copy build information"
            title="Copy build information"
        >
            {#if copied}<CheckIcon size={13} strokeWidth={2.5} aria-hidden="true" /><span>Copied</span>
            {:else}<CopyIcon size={13} strokeWidth={2} aria-hidden="true" /><span>Copy</span>{/if}
        </button>
    </div>

    <div class="updates-body">
        {#if phase === 'disabled'}
            <p class="updates-line muted">This is a development build, so automatic updates are turned off.</p>
        {:else if phase === 'checking'}
            <div class="updates-line"><LoaderIcon class="spin" size={16} aria-hidden="true" /> Checking for updates…</div>
        {:else if phase === 'installing'}
            <div class="updates-line"><LoaderIcon class="spin" size={16} aria-hidden="true" /> Installing update…</div>
        {:else if phase === 'installed'}
            <div class="updates-line"><LoaderIcon class="spin" size={16} aria-hidden="true" /> Update installed — restarting…</div>
        {:else if phase === 'downloading'}
            <div class="updates-status-row">
                <span class="updates-line">Downloading {latest?.version ?? ''}…</span>
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
            <div class="updates-headline">
                <span class="ready-dot" aria-hidden="true"></span>
                {latest?.version ?? 'Update'} is ready to install
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
                <div class="updates-headline">{latest?.version ?? 'An update'} is available</div>
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
                <p class="updates-line muted">{latest?.version ?? 'A newer version'} is available but skipped.</p>
                <button class="updates-link" type="button" onclick={clearSkippedVersion}>Undo skip</button>
            {:else}
                <p class="updates-line muted">{$updateState.install_hint || 'A newer version is available.'}</p>
                <button class="updates-link" type="button" onclick={openReleasePage}>
                    Get it from GitHub <ExternalLinkIcon size={12} strokeWidth={2} aria-hidden="true" />
                </button>
            {/if}
        {:else if phase === 'up_to_date'}
            <div class="updates-line ok"><CheckIcon size={16} strokeWidth={2.5} aria-hidden="true" /> You're on the latest version.</div>
        {:else}
            <p class="updates-line muted">Check whether a newer version of TDrive is available.</p>
        {/if}

        {#if $updateState.error && phase !== 'checking'}
            <div class="updates-error" role="alert">
                <CircleAlertIcon size={14} strokeWidth={2} aria-hidden="true" />
                <span>{$updateState.error}</span>
            </div>
        {/if}
    </div>

    {#if phase !== 'disabled'}
        <div class="updates-footer">
            <label class="updates-toggle">
                <input
                    type="checkbox"
                    checked={$updatePrefs.autoDownload}
                    onchange={(e) => setAutoDownload((e.currentTarget as HTMLInputElement).checked)}
                />
                <span>Download updates automatically</span>
            </label>
            <div class="updates-footer-row">
                <span class="updates-checked">{lastChecked}</span>
                <button
                    class="updates-link"
                    type="button"
                    onclick={() => void checkForUpdates({ explicit: true })}
                    disabled={phase === 'checking'}
                >
                    <RefreshCwIcon size={12} strokeWidth={2} aria-hidden="true" /> Check now
                </button>
            </div>
        </div>
    {/if}
</section>

<style>
    .updates-panel {
        width: min(360px, calc(100vw - 32px));
        color: var(--color-text);
    }

    .updates-header {
        padding: 8px 8px 14px;
        border-bottom: 1px solid var(--color-border-soft);
    }
    .updates-header h2 {
        margin: 0;
        font-size: 1.05rem;
        font-weight: 800;
        color: var(--color-text);
    }

    .updates-identity {
        display: grid;
        grid-template-columns: 1fr auto;
        align-items: center;
        gap: 4px 8px;
        padding: 14px 8px 12px;
    }
    .updates-app-name {
        grid-column: 1;
        font-size: 0.95rem;
        font-weight: 800;
    }
    .updates-app-meta {
        grid-column: 1;
        display: flex;
        flex-wrap: wrap;
        align-items: center;
        gap: 5px;
        color: var(--color-text-muted);
        font-size: 0.78rem;
    }
    .dot-sep { opacity: 0.6; }
    .updates-copy {
        grid-column: 2;
        grid-row: 1 / span 2;
        display: inline-flex;
        align-items: center;
        gap: 5px;
        padding: 5px 9px;
        color: var(--color-text-muted);
        background: var(--overlay-white-1);
        border: 1px solid var(--color-border-soft);
        border-radius: 8px;
        font-size: 0.72rem;
        font-weight: 700;
        cursor: pointer;
        transition: color var(--motion-fast) var(--ease-standard),
            background var(--motion-fast) var(--ease-standard);
    }
    .updates-copy:hover { color: var(--color-text); background: var(--overlay-white-2); }
    .updates-copy:focus-visible { outline: none; box-shadow: var(--focus-ring); }

    .updates-body { padding: 4px 8px 8px; }

    .updates-line {
        display: flex;
        align-items: center;
        gap: 8px;
        font-size: 0.86rem;
        line-height: 1.4;
    }
    .updates-line.muted { color: var(--color-text-muted); }
    .updates-line.ok { color: var(--color-success); font-weight: 650; }

    .updates-headline {
        display: flex;
        align-items: center;
        gap: 8px;
        font-size: 0.92rem;
        font-weight: 750;
        margin-bottom: 12px;
    }
    .ready-dot {
        width: 8px;
        height: 8px;
        border-radius: 50%;
        background: var(--color-accent);
        box-shadow: 0 0 0 4px var(--overlay-accent-1);
        flex-shrink: 0;
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

    .updates-actions { margin-bottom: 10px; }
    .updates-btn {
        display: inline-flex;
        align-items: center;
        justify-content: center;
        gap: 7px;
        padding: 9px 14px;
        border: 1px solid transparent;
        border-radius: 10px;
        font-size: 0.84rem;
        font-weight: 750;
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

    .updates-footer {
        margin-top: 4px;
        padding: 12px 8px 8px;
        border-top: 1px solid var(--color-border-soft);
    }
    .updates-toggle {
        display: flex;
        align-items: center;
        gap: 9px;
        font-size: 0.8rem;
        color: var(--color-text-soft);
        cursor: pointer;
    }
    .updates-toggle input { accent-color: var(--color-accent); cursor: pointer; }
    .updates-footer-row {
        display: flex;
        align-items: center;
        justify-content: space-between;
        margin-top: 10px;
    }
    .updates-checked { color: var(--color-text-subtle); font-size: 0.74rem; }

    :global(.updates-panel .spin) { animation: updates-spin 0.9s linear infinite; }
    @keyframes updates-spin { to { transform: rotate(360deg); } }

    @media (prefers-reduced-motion: reduce) {
        :global(.updates-panel .spin) { animation: none; }
        .updates-progress-fill { transition: none; }
    }
</style>
