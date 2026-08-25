<script lang="ts">
    import ModalShell from '../modals/ModalShell.svelte';
    import { mountSelection } from './mount-selection-store';

    const view = mountSelection.state;
</script>

<ModalShell
    hostId="mount-selection-modal"
    open={$view.open}
    title="Choose drives to mount"
    titleId="mount-selection-title"
    subtitle="Selected drives appear together in one TDrive volume."
    initialFocus=".mount-drive-choice input"
    restoreFocus="#profile-trigger"
    onClose={mountSelection.close}
>
    <fieldset class="mount-drive-list">
        <legend class="visually-hidden">Drives</legend>
        {#each $view.drives as drive (drive.id)}
            <label class="mount-drive-choice">
                <input
                    type="checkbox"
                    value={drive.id}
                    checked={$view.selectedIds.includes(drive.id)}
                    onchange={() => mountSelection.toggle(drive.id)}
                />
                <span class="mount-drive-copy">
                    <span class="mount-drive-title">{drive.title}</span>
                    <span class="mount-drive-mode">
                        {drive.kind === 'personal' ? 'Personal' : 'Shared · Read only'}
                    </span>
                </span>
            </label>
        {/each}
    </fieldset>

    {#snippet actions()}
        <button
            id="mount-selection-cancel"
            class="secondary-btn"
            type="button"
            onclick={mountSelection.close}
        >
            Cancel
        </button>
        <button
            id="mount-selection-confirm"
            class="primary-btn"
            type="button"
            disabled={$view.selectedIds.length === 0}
            onclick={mountSelection.confirm}
        >
            Mount
        </button>
    {/snippet}
</ModalShell>

<style>
    .mount-drive-list {
        display: grid;
        max-height: min(360px, 48vh);
        gap: var(--space-2);
        padding: 0;
        margin: 0 0 var(--space-4);
        overflow-y: auto;
        border: 0;
    }

    .mount-drive-choice {
        display: flex;
        align-items: center;
        gap: var(--space-3);
        min-width: 0;
        padding: var(--space-3);
        border: 1px solid var(--border);
        border-radius: var(--radius-md);
        color: var(--text-main);
        background: color-mix(in srgb, var(--text-main) 4%, transparent);
        cursor: pointer;
    }

    .mount-drive-choice:focus-within {
        border-color: var(--accent);
        box-shadow: var(--focus-ring);
    }

    .mount-drive-choice input {
        width: 18px;
        height: 18px;
        flex: 0 0 auto;
        margin: 0;
        accent-color: var(--accent);
    }

    .mount-drive-copy {
        display: flex;
        min-width: 0;
        flex-direction: column;
        gap: 2px;
    }

    .mount-drive-title {
        overflow: hidden;
        font-size: var(--font-size-sm);
        font-weight: 800;
        text-overflow: ellipsis;
        white-space: nowrap;
    }

    .mount-drive-mode {
        color: var(--text-muted);
        font-size: var(--font-size-xs);
    }

    .visually-hidden {
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

    :global(#mount-selection-confirm:disabled) {
        cursor: not-allowed;
        opacity: 0.55;
        transform: none;
    }
</style>
