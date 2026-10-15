<script lang="ts">
    interface Props { names: readonly string[]; destination?: string; queued?: boolean; label?: string }
    let { names, destination = '', queued = false, label = 'Selected files' }: Props = $props();
    const PREVIEW_LIMIT = 50;
</script>

{#if names.length || destination}
    <div class="selection">
        {#if destination}
            <div class="selection-destination"><span>Upload to</span><strong>{destination}</strong></div>
        {/if}
        {#if names.length}
            <!-- A half row shows at the bottom of a long list, which is the cue that it scrolls. -->
            <ul aria-label={label}>
                {#each names.slice(0, PREVIEW_LIMIT) as name, index (index)}<li title={name}>{name}</li>{/each}
            </ul>
            {#if names.length > PREVIEW_LIMIT}<p class="more">and {(names.length - PREVIEW_LIMIT).toLocaleString()} more</p>{/if}
        {/if}
    </div>
{/if}
{#if queued}
    <p class="queue-note">Starts after the current upload. Keep TDrive open; it pauses while another drive is open.</p>
{/if}

<style>
    .selection { margin: 0 0 var(--space-4); border: 1px solid var(--color-border-soft); border-radius: var(--radius-md); background: var(--overlay-neutral-1); overflow: hidden; }
    .selection-destination { display: flex; align-items: baseline; gap: var(--space-2); padding: var(--space-2-5) var(--space-3); font-size: var(--type-sm); }
    .selection-destination span { flex-shrink: 0; color: var(--color-text-muted); font-size: var(--type-xs); }
    .selection-destination strong { min-width: 0; font-weight: var(--weight-semibold); color: var(--color-text-soft); overflow-wrap: anywhere; }
    .selection-destination + ul { border-top: 1px solid var(--color-border-soft); }
    ul { --row: 28px; max-height: calc(var(--row) * 5.5 + var(--space-2)); margin: 0; padding: var(--space-1) 0; list-style: none; overflow: auto; overscroll-behavior: contain; }
    li { height: var(--row); padding: 0 var(--space-3); line-height: var(--row); font-size: var(--type-sm); color: var(--color-text-soft); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .more { margin: 0; padding: var(--space-2) var(--space-3); border-top: 1px solid var(--color-border-soft); color: var(--color-text-muted); font-size: var(--type-xs); }
    .queue-note { margin: calc(-1 * var(--space-2)) 0 var(--space-4); color: var(--color-text-muted); font-size: var(--type-xs); line-height: var(--leading-normal); }
</style>
