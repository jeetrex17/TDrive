<script lang="ts">
    import { mountLetter as defaultStore, type MountLetterStore } from './mount-letter-store';

    interface Props {
        store?: MountLetterStore;
    }

    let { store = defaultStore }: Props = $props();

    // A and B are reserved for floppy drives and C holds Windows itself.
    const LETTERS = 'DEFGHIJKLMNOPQRSTUVWXYZ'.split('').map((letter) => `${letter}:`);
    const options = $derived(LETTERS.includes($store.letter) ? LETTERS : [$store.letter, ...LETTERS]);
</script>

<div class="mount-letter-row">
    <div class="mount-letter-copy">
        <label class="mount-letter-title" for="mount-drive-letter">Drive letter</label>
        <div id="mount-drive-letter-description" class="mount-letter-description">
            Tdrive appears as this drive in File Explorer. A change applies the next time you mount.
        </div>
        {#if $store.error}
            <div class="mount-letter-error" role="alert">{$store.error}</div>
        {/if}
    </div>
    <select
        id="mount-drive-letter"
        class="mount-letter-select"
        aria-describedby="mount-drive-letter-description"
        value={$store.letter}
        disabled={$store.saving}
        onchange={(event) => void store.choose(event.currentTarget.value)}
    >
        {#each options as letter (letter)}
            <option value={letter}>{letter}</option>
        {/each}
    </select>
</div>

<style>
    .mount-letter-row {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: var(--space-4);
        padding: 12px 16px;
    }

    .mount-letter-copy { min-width: 0; }
    .mount-letter-title { color: var(--color-text); font-size: var(--type-sm); font-weight: var(--weight-medium); }
    .mount-letter-description { margin-top: 2px; color: var(--color-text-muted); font-size: var(--type-xs); line-height: 1.45; }
    .mount-letter-error { margin-top: 4px; color: var(--color-danger); font-size: var(--type-xs); }

    .mount-letter-select {
        flex: 0 0 auto;
        min-width: 72px;
        min-height: var(--control-md);
        padding: 0 var(--space-2);
        border: 1px solid var(--color-border-soft);
        border-radius: var(--radius-sm);
        background: var(--color-surface-1);
        color: var(--color-text);
        font: inherit;
        font-size: var(--type-sm);
        font-variant-numeric: tabular-nums;
        cursor: pointer;
    }

    .mount-letter-select:focus-visible { outline: none; box-shadow: var(--focus-ring); }
    .mount-letter-select:disabled { cursor: wait; opacity: 0.65; }
</style>
