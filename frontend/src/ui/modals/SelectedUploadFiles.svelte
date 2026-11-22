<script lang="ts">
    import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';

    interface Props { names: readonly string[]; destination?: string; queued?: boolean; label?: string }
    let { names, destination = '', queued = false, label = 'Selected files' }: Props = $props();
    const PREVIEW_LIMIT = 50;
    let expanded = $state(false);

    // One line says what is going where; the full list is a click away, so a
    // long selection never pushes the decision off the screen.
    const lead = $derived(names.length === 0 ? '' : names.length === 1 ? names[0]
        : names.length === 2 ? `${names[0]} and ${names[1]}`
            : `${names[0]} and ${(names.length - 1).toLocaleString()} more`);
</script>

{#if names.length || destination}
    <div class="selection">
        <p class="selection-line">
            {#if lead}<span class="selection-names" title={names.length === 1 ? names[0] : undefined}>{lead}</span>{/if}
            {#if destination}<span class="selection-destination">{lead ? 'to' : 'To'} <strong>{destination}</strong></span>{/if}
        </p>
        {#if names.length > 2}
            <button class="selection-toggle" type="button" aria-expanded={expanded} onclick={() => expanded = !expanded}>
                <ChevronRightIcon size={13} strokeWidth={2.25} aria-hidden="true" />
                {expanded ? 'Hide list' : `Show all ${names.length.toLocaleString()}`}
            </button>
            {#if expanded}
                <!-- A half row shows at the bottom of a long list, which is the cue that it scrolls. -->
                <ul aria-label={label}>
                    {#each names.slice(0, PREVIEW_LIMIT) as name, index (index)}<li title={name}>{name}</li>{/each}
                </ul>
                {#if names.length > PREVIEW_LIMIT}<p class="more">and {(names.length - PREVIEW_LIMIT).toLocaleString()} more</p>{/if}
            {/if}
        {/if}
    </div>
{/if}
{#if queued}
    <p class="queue-note">Starts after the current upload. Keep TDrive open; it pauses while another drive is open.</p>
{/if}

<style>
    .selection { margin: 0 0 var(--space-4); }
    .selection-line { display: flex; flex-wrap: wrap; align-items: baseline; gap: 0 var(--space-1); margin: 0; color: var(--color-text-muted); font-size: var(--type-sm); line-height: var(--leading-normal); }
    .selection-names { min-width: 0; max-width: 100%; overflow: hidden; color: var(--color-text-soft); text-overflow: ellipsis; white-space: nowrap; }
    .selection-destination strong { color: var(--color-text-soft); font-weight: var(--weight-medium); overflow-wrap: anywhere; }
    .selection-toggle { display: inline-flex; align-items: center; gap: 4px; margin: var(--space-1) 0 0 -4px; padding: 2px 4px; border: 0; border-radius: var(--radius-xs); background: transparent; color: var(--color-text-muted); font: inherit; font-size: var(--type-xs); cursor: pointer; }
    .selection-toggle :global(svg) { transition: transform var(--motion-fast) var(--ease-standard); }
    .selection-toggle[aria-expanded='true'] :global(svg) { transform: rotate(90deg); }
    .selection-toggle:focus-visible { outline: none; box-shadow: var(--focus-ring); }
    @media (hover: hover) { .selection-toggle:hover { color: var(--color-text); } }
    ul { --row: 26px; max-height: calc(var(--row) * 6.5); margin: var(--space-1) 0 0; padding: 0; border-left: 1px solid var(--color-border-soft); list-style: none; overflow: auto; overscroll-behavior: contain; }
    li { height: var(--row); padding: 0 var(--space-3); line-height: var(--row); font-size: var(--type-sm); color: var(--color-text-soft); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .more { margin: var(--space-1) 0 0; padding-left: var(--space-3); color: var(--color-text-muted); font-size: var(--type-xs); }
    .queue-note { margin: calc(-1 * var(--space-2)) 0 var(--space-4); color: var(--color-text-muted); font-size: var(--type-xs); line-height: var(--leading-normal); }
    @media (prefers-reduced-motion: reduce) { .selection-toggle :global(svg) { transition: none; } }
</style>
