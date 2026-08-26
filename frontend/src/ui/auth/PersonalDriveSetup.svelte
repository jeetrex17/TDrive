<script lang="ts">
    import CheckIcon from '@lucide/svelte/icons/check';
    import CloudIcon from '@lucide/svelte/icons/cloud';
    import HardDriveIcon from '@lucide/svelte/icons/hard-drive';
    import PlusIcon from '@lucide/svelte/icons/plus';
    import RotateCwIcon from '@lucide/svelte/icons/rotate-cw';
    import type { PersonalDriveCandidate, PersonalDrivePhase } from './personal-drive-store';

    interface Props {
        phase: PersonalDrivePhase;
        candidates: PersonalDriveCandidate[];
        error: string;
        createRetry?: boolean;
        onSelect: (channelID: string) => void;
        onCreate: () => void;
        onRetry: () => void;
    }

    let { phase, candidates, error, createRetry = false, onSelect, onCreate, onRetry }: Props = $props();
    let selectedID = $state('');
    let confirmingCreate = $state(false);

    const busy = $derived(phase === 'loading' || phase === 'recovering');

    $effect(() => {
        if (selectedID && !candidates.some((candidate) => candidate.id === selectedID)) {
            selectedID = '';
        }
        if (phase !== 'ready' || createRetry) confirmingCreate = false;
    });

    function formatCreated(timestamp: number): string {
        if (!Number.isFinite(timestamp) || timestamp <= 0) return 'Creation date unavailable';
        return `Created ${new Intl.DateTimeFormat('en-US', {
            month: 'short',
            day: 'numeric',
            year: 'numeric',
            timeZone: 'UTC',
        }).format(new Date(timestamp * 1000))}`;
    }

    function submitSelection(): void {
        if (!selectedID || busy) return;
        onSelect(selectedID);
    }
</script>

<section class="drive-setup" aria-labelledby="drive-setup-title">
    <div class="drive-setup-icon" aria-hidden="true">
        <HardDriveIcon size={28} strokeWidth={1.7} />
    </div>
    <header>
        <h2 id="drive-setup-title">Choose your TDrive</h2>
        <p>Pick a Telegram channel you created, or start with a new empty drive.</p>
    </header>

    {#if phase === 'loading'}
        <div class="drive-state" role="status" aria-live="polite">
            <span class="drive-spinner" aria-hidden="true"></span>
            <strong>Looking for your drives...</strong>
            <span>Checking your Telegram channels</span>
        </div>
    {:else if phase === 'discovery-error'}
        <div class="drive-error" role="alert">
            <strong>Couldn't load your channels</strong>
            <span>{error}</span>
        </div>
        <button class="drive-primary" data-drive-retry type="button" onclick={onRetry}>
            <RotateCwIcon size={17} aria-hidden="true" />
            Retry
        </button>
    {:else}
        {#if error}
            <div class="drive-inline-error" role="alert">{error}</div>
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
                        />
                        <span class="drive-choice-icon" aria-hidden="true">
                            <CloudIcon size={20} strokeWidth={1.8} />
                        </span>
                        <span class="drive-choice-copy">
                            <span class="drive-choice-title">
                                <span class="drive-title-text" title={candidate.title || 'Untitled channel'}>
                                    {candidate.title || 'Untitled channel'}
                                </span>
                                {#if candidate.recommended}
                                    <span class="drive-badge">Recommended</span>
                                {/if}
                            </span>
                            <span class="drive-choice-meta">
                                <span>{formatCreated(candidate.created_at)}</span>
                                <span aria-hidden="true">/</span>
                                <span>Channel ID {candidate.id}</span>
                            </span>
                            <span class:active={candidate.has_activity} class="drive-activity">
                                {candidate.has_activity ? 'Has activity' : 'Empty'}
                            </span>
                        </span>
                        <span class="drive-check" aria-hidden="true">
                            <CheckIcon size={14} strokeWidth={2.5} />
                        </span>
                    </label>
                {/each}
            </fieldset>

            <button
                class="drive-primary"
                data-drive-continue
                type="button"
                disabled={!selectedID || busy}
                onclick={submitSelection}
            >
                {phase === 'recovering' ? 'Recovering...' : 'Continue'}
            </button>
        {:else}
            <div class="drive-empty">
                <CloudIcon size={25} strokeWidth={1.6} aria-hidden="true" />
                <strong>No personal channels found</strong>
                <span>You can create a new private home for TDrive.</span>
            </div>
        {/if}

        {#if phase === 'recovering'}
            <div class="drive-progress" role="status" aria-live="polite">
                <span class="drive-spinner" aria-hidden="true"></span>
                Recovering your TDrive history...
            </div>
        {/if}

        {#if createRetry}
            <div class="drive-create-retry">
                <button
                    class="drive-create"
                    data-drive-create-retry
                    type="button"
                    disabled={busy}
                    onclick={onCreate}
                >
                    <RotateCwIcon size={17} strokeWidth={2} aria-hidden="true" />
                    Retry TDrive Setup
                </button>
                <span>Continues the previous attempt without creating a duplicate channel.</span>
            </div>
        {:else if confirmingCreate}
            <div class="drive-confirm" role="group" aria-label="Confirm new TDrive">
                <div>
                    <strong>Create a new empty TDrive?</strong>
                    <span>This creates one new Telegram channel.</span>
                </div>
                <div class="drive-confirm-actions">
                    <button type="button" disabled={busy} onclick={() => { confirmingCreate = false; }}>Cancel</button>
                    <button data-drive-create-confirm type="button" disabled={busy} onclick={onCreate}>Create</button>
                </div>
            </div>
        {:else}
            <button
                class="drive-create"
                data-drive-create-request
                type="button"
                disabled={busy}
                onclick={() => { confirmingCreate = true; }}
            >
                <PlusIcon size={17} strokeWidth={2} aria-hidden="true" />
                Create New TDrive
            </button>
        {/if}
    {/if}
</section>

<style>
    .drive-setup {
        width: min(620px, calc(100vw - 32px));
        max-height: calc(100vh - 64px);
        padding: 32px;
        overflow: auto;
        color: var(--color-text);
        text-align: center;
        background: color-mix(in srgb, var(--bg-sidebar) 94%, transparent);
        border: 1px solid var(--border);
        border-radius: 22px;
        box-shadow: var(--shadow-lg), 0 1px 0 var(--glass-highlight) inset;
        -webkit-backdrop-filter: blur(24px) saturate(145%);
        backdrop-filter: blur(24px) saturate(145%);
    }

    .drive-setup-icon {
        display: grid;
        width: 56px;
        height: 56px;
        margin: 0 auto 18px;
        color: var(--color-accent);
        background: var(--overlay-accent-1);
        border: 1px solid color-mix(in srgb, var(--color-accent) 24%, transparent);
        border-radius: 16px;
        place-items: center;
    }

    header h2 { margin: 0; font-size: 1.55rem; font-weight: 680; letter-spacing: 0; }
    header p { max-width: 440px; margin: 8px auto 22px; color: var(--color-text-muted); font-size: 0.91rem; line-height: 1.5; }

    .drive-list {
        display: grid;
        max-height: min(342px, 42vh);
        padding: 0;
        margin: 0 0 16px;
        overflow: auto;
        text-align: left;
        border: 1px solid var(--border);
        border-radius: 14px;
    }

    .drive-choice {
        position: relative;
        display: grid;
        grid-template-columns: 34px minmax(0, 1fr) 24px;
        gap: 12px;
        align-items: center;
        min-height: 74px;
        padding: 12px 14px;
        cursor: pointer;
        background: var(--color-surface-1);
        border-bottom: 1px solid var(--border);
        transition: background var(--motion-fast) var(--ease-standard);
    }

    .drive-choice:first-of-type { border-radius: 13px 13px 0 0; }
    .drive-choice:last-of-type { border-bottom: 0; border-radius: 0 0 13px 13px; }
    .drive-choice:hover { background: var(--color-surface-2); }
    .drive-choice.selected { background: var(--overlay-accent-1); }
    .drive-choice:has(input:focus-visible) { box-shadow: var(--focus-ring) inset; }
    .drive-choice input { position: absolute; width: 1px; height: 1px; margin: -1px; opacity: 0; }

    .drive-choice-icon {
        display: grid;
        width: 34px;
        height: 34px;
        color: var(--color-text-soft);
        background: var(--overlay-white-1);
        border-radius: 10px;
        place-items: center;
    }

    .drive-choice-copy { display: grid; min-width: 0; gap: 4px; }
    .drive-choice-title { display: flex; gap: 7px; align-items: center; min-width: 0; overflow: hidden; font-size: 0.94rem; font-weight: 650; }
    .drive-title-text { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .drive-badge { flex: 0 0 auto; padding: 3px 7px; color: var(--color-accent); font-size: 0.66rem; font-weight: 700; background: var(--overlay-accent-1); border-radius: 999px; }
    .drive-choice-meta { display: flex; flex-wrap: wrap; gap: 5px; color: var(--color-text-muted); font-size: 0.72rem; }
    .drive-activity { width: fit-content; color: var(--color-text-muted); font-size: 0.69rem; }
    .drive-activity.active { color: var(--color-success, #34c759); }

    .drive-check {
        display: grid;
        width: 20px;
        height: 20px;
        color: transparent;
        border: 1.5px solid var(--border-strong, var(--border));
        border-radius: 50%;
        place-items: center;
    }
    .selected .drive-check { color: white; background: var(--color-accent); border-color: var(--color-accent); }

    .drive-primary,
    .drive-create,
    .drive-confirm-actions button {
        display: inline-flex;
        align-items: center;
        justify-content: center;
        gap: 8px;
        min-height: 42px;
        padding: 0 18px;
        font: inherit;
        font-size: 0.88rem;
        font-weight: 650;
        border-radius: 11px;
        cursor: pointer;
        transition: transform var(--motion-fast) var(--ease-standard), opacity var(--motion-fast) var(--ease-standard), background var(--motion-fast) var(--ease-standard);
    }
    .drive-primary { width: 100%; color: white; background: var(--color-accent); border: 1px solid transparent; }
    .drive-primary:not(:disabled):hover { filter: brightness(1.06); }
    .drive-primary:not(:disabled):active, .drive-create:not(:disabled):active { transform: scale(0.985); }
    button:focus-visible { outline: none; box-shadow: var(--focus-ring); }
    button:disabled { cursor: default; opacity: 0.48; }

    .drive-create { margin-top: 12px; color: var(--color-text-soft); background: transparent; border: 0; }
    .drive-create:not(:disabled):hover { color: var(--color-accent); background: var(--overlay-accent-1); }
    .drive-create-retry { display: grid; justify-items: center; gap: 2px; }
    .drive-create-retry span { max-width: 360px; color: var(--color-text-muted); font-size: 0.72rem; line-height: 1.4; }

    .drive-state,
    .drive-empty {
        display: grid;
        justify-items: center;
        gap: 7px;
        padding: 34px 20px;
        margin-bottom: 15px;
        color: var(--color-text-muted);
        background: var(--color-surface-1);
        border: 1px solid var(--border);
        border-radius: 14px;
    }
    .drive-state strong, .drive-empty strong { color: var(--color-text); font-size: 0.93rem; }
    .drive-state span, .drive-empty span { font-size: 0.78rem; }

    .drive-error,
    .drive-inline-error {
        display: grid;
        gap: 5px;
        padding: 13px 14px;
        margin-bottom: 14px;
        color: var(--color-danger, #ff453a);
        text-align: left;
        background: color-mix(in srgb, var(--color-danger, #ff453a) 9%, transparent);
        border: 1px solid color-mix(in srgb, var(--color-danger, #ff453a) 24%, transparent);
        border-radius: 11px;
        font-size: 0.8rem;
    }
    .drive-error span { color: var(--color-text-muted); }

    .drive-progress { display: flex; gap: 8px; align-items: center; justify-content: center; margin-top: 10px; color: var(--color-text-muted); font-size: 0.78rem; }
    .drive-spinner { width: 16px; height: 16px; border: 2px solid var(--border); border-top-color: var(--color-accent); border-radius: 50%; animation: drive-spin 800ms linear infinite; }

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
        border-radius: 12px;
    }
    .drive-confirm > div:first-child { display: grid; gap: 3px; }
    .drive-confirm strong { font-size: 0.82rem; }
    .drive-confirm span { color: var(--color-text-muted); font-size: 0.72rem; }
    .drive-confirm-actions { display: flex; gap: 7px; }
    .drive-confirm-actions button { min-height: 34px; padding: 0 11px; color: var(--color-text-soft); background: var(--color-surface-2); border: 1px solid var(--border); font-size: 0.76rem; }
    .drive-confirm-actions button:last-child { color: white; background: var(--color-accent); border-color: transparent; }

    .sr-only { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; border: 0; }

    @keyframes drive-spin { to { transform: rotate(360deg); } }

    @media (max-width: 560px) {
        .drive-setup { max-height: calc(100vh - 24px); padding: 24px 18px; border-radius: 18px; }
        .drive-choice { grid-template-columns: 32px minmax(0, 1fr) 22px; gap: 10px; padding: 11px; }
        .drive-confirm { align-items: stretch; flex-direction: column; }
        .drive-confirm-actions button { flex: 1; }
    }

    @media (prefers-reduced-motion: reduce) {
        .drive-spinner { animation-duration: 1.5s; }
        .drive-choice, button { transition: none; }
    }
</style>
