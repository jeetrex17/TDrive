<script lang="ts">
    import CheckIcon from '@lucide/svelte/icons/check';
    import HardDriveIcon from '@lucide/svelte/icons/hard-drive';
    import PlusIcon from '@lucide/svelte/icons/plus';
    import RotateCwIcon from '@lucide/svelte/icons/rotate-cw';
    import { isMobilePlatform } from '../../api';
    import type { PersonalDriveCandidate } from '../../types';
    import type { DriveScanProgress, PersonalDrivePhase } from './personal-drive-store';

    interface Props {
        phase: PersonalDrivePhase;
        candidates: PersonalDriveCandidate[];
        error: string;
        detail?: string;
        createRetry?: boolean;
        scan?: DriveScanProgress | null;
        waitSeconds?: number;
        onSelect: (channelID: string) => void;
        onCreate: () => void;
        onRetry: () => void | Promise<void>;
    }

    let {
        phase,
        candidates,
        error,
        detail = '',
        createRetry = false,
        scan = null,
        waitSeconds = 0,
        onSelect,
        onCreate,
        onRetry,
    }: Props = $props();

    // Phones move the primary and create actions into the bottom bar; the
    // desktop card keeps them in reading order under the list. With no
    // channel to pick, creating is the only way forward and takes the
    // primary look there.
    const mobile = isMobilePlatform();
    const createIsPrimary = $derived(mobile && candidates.length === 0);

    let selectedID = $state('');
    let confirmingCreate = $state(false);
    let retrying = $state(false);

    const busy = $derived(phase === 'loading' || phase === 'recovering');
    const recovering = $derived(phase === 'recovering');

    // Counting walks the channel backwards to size the job, so no total is
    // known yet; only the applying pass can fill a determinate bar.
    const percent = $derived.by(() => {
        if (!scan || scan.phase !== 'applying' || scan.messages_total <= 0) return null;
        const ratio = scan.messages_done / scan.messages_total;
        return Math.max(0, Math.min(100, Math.round(ratio * 100)));
    });

    let waitRemaining = $state(0);

    // Telegram's pauses are the only stage long enough to look like a hang,
    // so count them down locally rather than showing a frozen number.
    $effect(() => {
        if (waitSeconds <= 0) {
            waitRemaining = 0;
            return;
        }
        let left = waitSeconds;
        waitRemaining = left;
        const timer = setInterval(() => {
            left -= 1;
            waitRemaining = Math.max(0, left);
            if (left <= 0) clearInterval(timer);
        }, 1000);
        return () => clearInterval(timer);
    });

    const scanLabel = $derived.by(() => {
        if (waitRemaining > 0) return `Telegram asked us to slow down, resuming in ${waitRemaining}s`;
        if (!scan) return 'Reading your Telegram channel…';
        if (scan.phase === 'counting') {
            return `Counting messages… ${formatCount(scan.messages_done)} so far`;
        }
        return `Rebuilding your files… ${formatCount(scan.messages_done)} of ${formatCount(scan.messages_total)} messages`;
    });

    $effect(() => {
        if (selectedID && !candidates.some((candidate) => candidate.id === selectedID)) {
            selectedID = '';
        }
        if (phase !== 'ready' || createRetry) confirmingCreate = false;
    });

    const createdFormat = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' });

    function candidateTitle(candidate: PersonalDriveCandidate): string {
        return candidate.title || 'Untitled channel';
    }

    function formatCount(value: number): string {
        return value.toLocaleString();
    }

    function formatCreated(timestamp: number): string {
        if (!Number.isFinite(timestamp) || timestamp <= 0) return '';
        return `Created ${createdFormat.format(new Date(timestamp * 1000))}`;
    }

    function submitSelection(): void {
        if (!selectedID || busy) return;
        onSelect(selectedID);
    }

    function onChoiceKeydown(event: KeyboardEvent): void {
        if (event.key !== 'Enter') return;
        event.preventDefault();
        submitSelection();
    }

    // Discovery only changes phase once the backend answers, so the button
    // guards itself against a second tap until then.
    async function retry(): Promise<void> {
        if (retrying) return;
        retrying = true;
        try {
            await onRetry();
        } finally {
            retrying = false;
        }
    }
</script>

{#snippet continueButton()}
    <button
        class="primary-btn drive-primary"
        class:is-busy={recovering}
        data-drive-continue
        type="button"
        disabled={!selectedID || busy}
        onclick={submitSelection}
    >
        {recovering ? 'Recovering…' : mobile ? 'Use this drive' : 'Continue'}
    </button>
{/snippet}

{#snippet retryButton()}
    <button
        class="primary-btn drive-primary"
        class:is-busy={retrying}
        data-drive-retry
        type="button"
        disabled={retrying}
        onclick={() => void retry()}
    >
        <RotateCwIcon size={16} strokeWidth={2.2} aria-hidden="true" />
        {retrying ? 'Retrying…' : 'Retry'}
    </button>
{/snippet}

{#snippet createActions()}
    {#if createRetry}
        <div class="drive-secondary drive-secondary-stack">
            <button
                class="drive-ghost"
                class:is-primary={createIsPrimary}
                data-drive-create-retry
                type="button"
                disabled={busy}
                onclick={onCreate}
            >
                <RotateCwIcon size={16} strokeWidth={2.2} aria-hidden="true" />
                Retry TDrive Setup
            </button>
            <span class="drive-hint">Continues the previous attempt without creating a duplicate channel.</span>
        </div>
    {:else if confirmingCreate}
        <div class="drive-confirm" role="group" aria-label="Confirm new TDrive">
            <div>
                <strong>Create a new empty TDrive?</strong>
                <span>This creates one new Telegram channel.</span>
            </div>
            <div class="drive-confirm-actions">
                <button class="drive-ghost" type="button" disabled={busy} onclick={() => { confirmingCreate = false; }}>
                    Cancel
                </button>
                <button class="drive-confirm-create" data-drive-create-confirm type="button" disabled={busy} onclick={onCreate}>
                    Create
                </button>
            </div>
        </div>
    {:else}
        <button
            class="drive-ghost drive-secondary"
            class:is-primary={createIsPrimary}
            data-drive-create-request
            type="button"
            disabled={busy}
            onclick={() => { confirmingCreate = true; }}
        >
            <PlusIcon size={16} strokeWidth={2.2} aria-hidden="true" />
            Create New TDrive
        </button>
    {/if}
{/snippet}

<section class="auth-box drive-setup" aria-labelledby="drive-setup-title">
    <div class="auth-page-body">
        <div class="auth-icon-box">
            <HardDriveIcon size={32} strokeWidth={1.5} aria-hidden="true" />
        </div>
        <h2 id="drive-setup-title">Choose your TDrive</h2>
        <p>No saved drive was found on this device. Pick the Telegram channel that holds your files, or start a new empty drive.</p>

        {#if phase === 'loading'}
            <div class="drive-panel" role="status" aria-live="polite">
                <span class="drive-spinner" aria-hidden="true"></span>
                <strong>Looking for your drives…</strong>
                <span>Checking the channels you created on Telegram</span>
            </div>
        {:else if phase === 'discovery-error'}
            <div class="drive-alert" role="alert">
                <strong>{error}</strong>
                {#if detail}<span>{detail}</span>{/if}
            </div>
            {#if !mobile}
                {@render retryButton()}
            {/if}
        {:else}
            {#if error}
                <div class="drive-alert" role="alert">
                    <strong>{error}</strong>
                    {#if detail}<span>{detail}</span>{/if}
                </div>
            {/if}

            {#if candidates.length > 0}
                <fieldset class="drive-list" disabled={busy}>
                    <legend class="sr-only">Your Telegram channels</legend>
                    {#each candidates as candidate (candidate.id)}
                        <label class="drive-choice" class:selected={selectedID === candidate.id}>
                            <input
                                type="radio"
                                name="personal-drive"
                                value={candidate.id}
                                bind:group={selectedID}
                                disabled={busy}
                                onkeydown={onChoiceKeydown}
                            />
                            <span class="drive-choice-copy">
                                <span class="drive-choice-title">
                                    <span class="drive-title-text" title={candidateTitle(candidate)}>
                                        {candidateTitle(candidate)}
                                    </span>
                                    {#if candidate.recommended}
                                        <span class="drive-badge">Recommended</span>
                                    {/if}
                                </span>
                                <span class="drive-choice-meta">
                                    <span class:in-use={candidate.hasActivity}>
                                        {candidate.hasActivity ? 'In use' : 'Empty'}
                                    </span>
                                    {#if formatCreated(candidate.createdAt)}
                                        <span>{formatCreated(candidate.createdAt)}</span>
                                    {/if}
                                    <span>ID {candidate.id}</span>
                                </span>
                            </span>
                            <span class="drive-check" aria-hidden="true">
                                <CheckIcon size={13} strokeWidth={3} />
                            </span>
                        </label>
                    {/each}
                </fieldset>

                {#if !mobile}
                    {@render continueButton()}
                {/if}
            {:else}
                <div class="drive-panel">
                    <strong>No channels found</strong>
                    <span>You haven't created any Telegram channels, so there is nothing to recover yet.</span>
                </div>
            {/if}

            {#if recovering}
                <div class="drive-scan" data-drive-scan>
                    <div
                        class="drive-scan-track"
                        role="progressbar"
                        aria-label="Recovery progress"
                        aria-valuemin={0}
                        aria-valuemax={100}
                        aria-valuenow={percent ?? undefined}
                    >
                        <span
                            class="drive-scan-fill"
                            class:indeterminate={percent === null}
                            style={percent === null ? undefined : `width: ${percent}%`}
                        ></span>
                    </div>
                    <span class="drive-hint" role="status" aria-live="polite">{scanLabel}</span>
                </div>
            {/if}

            {#if !mobile}
                {@render createActions()}
            {/if}
        {/if}
    </div>

    {#if mobile && phase !== 'loading'}
        <div class="auth-actions">
            {#if phase === 'discovery-error'}
                {@render retryButton()}
            {:else}
                {#if candidates.length > 0}
                    {@render continueButton()}
                {/if}
                {@render createActions()}
            {/if}
        </div>
    {/if}
</section>

<style>
    /* Extends the shared .auth-box card: same surface, border, radius and
       shadow as the login screens, just wide enough for a channel list. */
    .drive-setup {
        width: min(460px, calc(100vw - 32px));
        max-height: calc(100vh - 48px);
        padding: 2.25rem 2rem;
        overflow: auto;
    }
    .auth-page-body > p {
        max-width: 380px;
        margin-right: auto;
        margin-left: auto;
        line-height: 1.5;
    }

    .drive-list {
        display: grid;
        max-height: min(320px, 40vh);
        padding: 0;
        margin: 0 0 16px;
        overflow: auto;
        text-align: left;
        border: 1px solid var(--border);
        border-radius: var(--radius-md);
    }

    .drive-choice {
        position: relative;
        display: grid;
        grid-template-columns: minmax(0, 1fr) 20px;
        gap: 12px;
        align-items: center;
        padding: 12px 14px;
        cursor: pointer;
        background: var(--color-surface-1);
        transition: background var(--motion-fast) var(--ease-standard);
    }
    .drive-choice + .drive-choice { border-top: 1px solid var(--border); }
    .drive-choice:hover { background: var(--color-surface-2); }
    .drive-choice.selected { background: var(--overlay-accent-1); }
    .drive-choice:has(input:focus-visible) { box-shadow: var(--focus-ring) inset; }
    .drive-choice input {
        position: absolute;
        width: 1px;
        height: 1px;
        margin: -1px;
        opacity: 0;
        pointer-events: none;
    }

    .drive-choice-copy { display: grid; gap: 3px; min-width: 0; }
    .drive-choice-title {
        display: flex;
        gap: 8px;
        align-items: center;
        min-width: 0;
        color: var(--color-text);
        font-size: 0.92rem;
        font-weight: 600;
    }
    .drive-title-text { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .drive-badge {
        flex: 0 0 auto;
        padding: 2px 7px;
        color: var(--accent);
        font-size: 0.66rem;
        font-weight: 700;
        letter-spacing: 0.02em;
        background: var(--overlay-accent-1);
        border-radius: var(--radius-pill);
    }
    .drive-choice-meta { display: flex; flex-wrap: wrap; color: var(--text-muted); font-size: 0.76rem; }
    .drive-choice-meta > span + span::before { margin: 0 6px; content: '·'; }
    .drive-choice-meta .in-use { color: var(--color-success); font-weight: 600; }

    .drive-check {
        display: grid;
        width: 20px;
        height: 20px;
        color: transparent;
        border: 1.5px solid color-mix(in srgb, var(--text-muted) 55%, transparent);
        border-radius: 50%;
        place-items: center;
        transition:
            background var(--motion-fast) var(--ease-standard),
            border-color var(--motion-fast) var(--ease-standard);
    }
    .selected .drive-check {
        color: var(--color-on-accent);
        background: var(--accent);
        border-color: var(--accent);
    }

    .drive-primary { display: inline-flex; gap: 8px; align-items: center; justify-content: center; }
    .drive-primary:disabled { opacity: 0.5; cursor: default; transform: none; }
    .drive-primary:disabled:hover { background: var(--accent); }

    .drive-hint {
        display: block;
        margin-top: 10px;
        color: var(--text-muted);
        font-size: 0.78rem;
        line-height: 1.45;
    }

    .drive-scan { display: grid; gap: 7px; margin-top: 14px; }
    .drive-scan .drive-hint { margin-top: 0; }

    .drive-scan-track {
        height: 4px;
        overflow: hidden;
        background: var(--color-surface-2);
        border-radius: var(--radius-pill);
    }
    .drive-scan-fill {
        display: block;
        width: 0;
        height: 100%;
        background: var(--accent);
        border-radius: inherit;
        transition: width var(--motion-med) var(--ease-standard);
    }
    .drive-scan-fill.indeterminate {
        width: 38%;
        animation: drive-scan-slide 1.25s var(--ease-standard) infinite;
    }

    .drive-panel {
        display: grid;
        gap: 6px;
        justify-items: center;
        padding: 28px 20px;
        margin-bottom: 16px;
        color: var(--text-muted);
        font-size: 0.8rem;
        background: var(--color-surface-1);
        border: 1px solid var(--border);
        border-radius: var(--radius-md);
    }
    .drive-panel strong { color: var(--color-text); font-size: 0.92rem; }

    .drive-alert {
        display: grid;
        gap: 4px;
        padding: 12px 14px;
        margin-bottom: 14px;
        color: var(--color-danger);
        font-size: 0.82rem;
        text-align: left;
        background: color-mix(in srgb, var(--color-danger) 8%, transparent);
        border: 1px solid color-mix(in srgb, var(--color-danger) 22%, transparent);
        border-radius: var(--radius-md);
    }
    .drive-alert span { color: var(--text-muted); font-size: 0.76rem; overflow-wrap: anywhere; }

    .drive-spinner {
        width: 16px;
        height: 16px;
        border: 2px solid var(--border);
        border-top-color: var(--accent);
        border-radius: 50%;
        animation: drive-spin 800ms linear infinite;
    }

    .drive-ghost {
        display: inline-flex;
        gap: 6px;
        align-items: center;
        justify-content: center;
        min-height: 36px;
        padding: 0 12px;
        color: var(--color-text-soft);
        font: inherit;
        font-size: 0.85rem;
        font-weight: 600;
        background: transparent;
        border: 0;
        border-radius: var(--radius-md);
        cursor: pointer;
        transition:
            color var(--motion-fast) var(--ease-standard),
            background var(--motion-fast) var(--ease-standard);
    }
    .drive-ghost:not(:disabled):hover { color: var(--accent); background: var(--overlay-accent-1); }
    .drive-secondary { margin-top: 12px; }
    .drive-secondary-stack { display: grid; gap: 2px; justify-items: center; }
    .drive-secondary-stack .drive-hint { margin-top: 0; }

    .drive-confirm {
        display: flex;
        gap: 14px;
        align-items: center;
        justify-content: space-between;
        padding: 12px 14px;
        margin-top: 12px;
        text-align: left;
        background: var(--color-surface-1);
        border: 1px solid var(--border);
        border-radius: var(--radius-md);
    }
    .drive-confirm > div:first-child { display: grid; gap: 2px; }
    .drive-confirm strong { color: var(--color-text); font-size: 0.84rem; }
    .drive-confirm span { color: var(--text-muted); font-size: 0.74rem; }
    .drive-confirm-actions { display: flex; flex: 0 0 auto; gap: 6px; }
    .drive-confirm-create {
        min-height: 34px;
        padding: 0 14px;
        color: var(--color-on-accent);
        font: inherit;
        font-size: 0.8rem;
        font-weight: 700;
        background: var(--accent);
        border: 0;
        border-radius: var(--radius-md);
        cursor: pointer;
    }

    button:disabled { opacity: 0.5; cursor: default; }
    button:focus-visible { outline: none; box-shadow: var(--focus-ring); }

    .sr-only {
        position: absolute;
        width: 1px;
        height: 1px;
        padding: 0;
        margin: -1px;
        overflow: hidden;
        clip: rect(0, 0, 0, 0);
        white-space: nowrap;
        border: 0;
    }

    @keyframes drive-spin { to { transform: rotate(360deg); } }
    @keyframes drive-scan-slide {
        from { transform: translateX(-100%); }
        to { transform: translateX(300%); }
    }

    @media (max-width: 520px) {
        .drive-setup { padding: 1.75rem 1.25rem; }
        .drive-confirm { flex-direction: column; align-items: stretch; }
        .drive-confirm-actions button { flex: 1; }
    }

    @media (prefers-reduced-motion: reduce) {
        .drive-spinner { animation-duration: 1.5s; }
        .drive-scan-fill { transition: none; }
        .drive-scan-fill.indeterminate { width: 100%; animation: none; }
        .drive-choice, .drive-check, .drive-ghost { transition: none; }
    }
    /* Phone page: the list scrolls with the body and the actions sit in the
       bottom bar (auth.css owns the page frame; this tunes the picker). */
    :global(html.mobile) .drive-setup {
        width: 100%;
        max-height: none;
        padding: 0;
        overflow: hidden;
    }
    :global(html.mobile) .auth-page-body > p {
        max-width: none;
        margin: 0 0 1.5rem;
    }
    :global(html.mobile) .drive-list {
        max-height: none;
        margin: 0;
        overflow: hidden;
        border-radius: var(--radius-lg);
    }
    :global(html.mobile) .drive-choice {
        min-height: 56px;
        padding: 12px 16px;
    }
    :global(html.mobile) .drive-choice-title { font-size: 0.9375rem; }
    :global(html.mobile) .drive-choice-meta { font-size: 0.8125rem; }
    :global(html.mobile) .drive-badge { font-size: 0.75rem; }
    :global(html.mobile) .drive-check { width: 22px; height: 22px; }
    :global(html.mobile) .drive-panel {
        padding: 32px 20px;
        margin: 0;
        font-size: 0.875rem;
        border-radius: var(--radius-lg);
    }
    :global(html.mobile) .drive-panel strong { font-size: 1rem; }
    :global(html.mobile) .drive-alert {
        font-size: 0.9375rem;
        border-radius: var(--radius-lg);
    }
    :global(html.mobile) .drive-alert span { font-size: 0.8125rem; }
    :global(html.mobile) .drive-scan { margin-top: 16px; }
    :global(html.mobile) .drive-hint { font-size: 0.8125rem; line-height: 1.5; }
    :global(html.mobile) .drive-primary {
        min-height: 50px;
        font-size: 1rem;
        border-radius: var(--radius-lg);
    }
    :global(html.mobile) .drive-primary:hover { transform: none; }
    :global(html.mobile) .drive-primary.is-busy::before {
        width: 16px;
        height: 16px;
        content: '';
        border: 2px solid currentColor;
        border-top-color: transparent;
        border-radius: 50%;
        animation: drive-spin 720ms linear infinite;
    }
    :global(html.mobile) .drive-primary.is-busy > :global(svg) { display: none; }
    :global(html.mobile) .drive-ghost {
        min-height: 44px;
        font-size: 0.9375rem;
    }
    :global(html.mobile) .drive-secondary {
        width: 100%;
        margin-top: 4px;
    }
    :global(html.mobile) .drive-ghost.is-primary {
        width: 100%;
        min-height: 50px;
        margin-top: 0;
        color: var(--color-on-accent);
        font-size: 1rem;
        font-weight: var(--weight-strong);
        background: var(--accent);
        border-radius: var(--radius-lg);
    }
    :global(html.mobile) .drive-ghost.is-primary:not(:disabled):hover {
        color: var(--color-on-accent);
        background: var(--accent);
    }
    :global(html.mobile) .drive-secondary-stack { gap: 0; }
    :global(html.mobile) .drive-confirm {
        flex-direction: column;
        align-items: stretch;
        margin-top: 4px;
        border-radius: var(--radius-lg);
    }
    :global(html.mobile) .drive-confirm strong { font-size: 0.9375rem; }
    :global(html.mobile) .drive-confirm span { font-size: 0.8125rem; }
    :global(html.mobile) .drive-confirm-actions button {
        flex: 1;
        min-height: 44px;
    }
    :global(html.mobile) .drive-confirm-create {
        font-size: 0.9375rem;
        border-radius: var(--radius-md);
    }
</style>
