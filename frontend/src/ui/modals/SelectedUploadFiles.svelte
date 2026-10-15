<script lang="ts">
    interface Props { names: readonly string[]; label?: string }
    let { names, label = 'Selected files' }: Props = $props();
    const PREVIEW_LIMIT = 50;
</script>

{#if names.length}
    <ul aria-label={label}>
        {#each names.slice(0, PREVIEW_LIMIT) as name, index (index)}<li>{name}</li>{/each}
    </ul>
    {#if names.length > PREVIEW_LIMIT}<p>and {names.length - PREVIEW_LIMIT} more selected items</p>{/if}
{/if}

<style>
    ul { max-height: min(25dvh, 10rem); overflow: auto; overscroll-behavior: contain; padding-inline-start: var(--space-5); margin-block: var(--space-3); font-size: var(--type-sm); }
    li { overflow-wrap: anywhere; padding-block: var(--space-1); }
    p { font-size: var(--type-xs); color: var(--color-text-muted); }
</style>
