<script lang="ts">
    import { tick } from 'svelte';
    import ArrowDownUpIcon from '@lucide/svelte/icons/arrow-down-up';
    import CheckIcon from '@lucide/svelte/icons/check';
    import { pushSheet } from '../modals/sheet-stack';
    import { CHANNEL_SORTS, type ChannelSort } from './channel-model';

    interface Props {
        value: ChannelSort;
        onChange: (sort: ChannelSort) => void;
        /** The phone's top bar has room for the icon alone. */
        iconOnly?: boolean;
    }

    let { value, onChange, iconOnly = false }: Props = $props();
    let open = $state(false);
    let trigger = $state<HTMLButtonElement | null>(null);
    let menu = $state<HTMLElement | null>(null);
    const label = $derived(CHANNEL_SORTS.find((option) => option.value === value)?.label ?? 'Newest');

    function options(): HTMLButtonElement[] {
        return Array.from(menu?.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]') ?? []);
    }

    async function toggle(): Promise<void> {
        if (open) {
            close(true);
            return;
        }
        open = true;
        await tick();
        options().find((option) => option.getAttribute('aria-checked') === 'true')?.focus();
    }

    function close(restoreFocus = false): void {
        if (!open) return;
        open = false;
        if (restoreFocus) trigger?.focus();
    }

    function choose(sort: ChannelSort): void {
        close(true);
        if (sort !== value) onChange(sort);
    }

    function onMenuKeydown(event: KeyboardEvent): void {
        if (event.key === 'Escape') {
            event.preventDefault();
            event.stopPropagation();
            close(true);
            return;
        }
        if (event.key === 'Tab') {
            close();
            return;
        }
        const list = options();
        const index = list.indexOf(document.activeElement as HTMLButtonElement);
        const next = { ArrowDown: index + 1, ArrowUp: index - 1, Home: 0, End: list.length - 1 }[event.key];
        if (next === undefined) return;
        event.preventDefault();
        list[(next + list.length) % list.length]?.focus();
    }

    function onDocumentClick(event: MouseEvent): void {
        const target = event.target as Node;
        if (open && !trigger?.contains(target) && !menu?.contains(target)) close();
    }

    // Android BACK closes the menu before it reaches the channel underneath.
    $effect(() => {
        if (!open) return;
        const back = pushSheet(() => close(true));
        return () => back.release();
    });
</script>

<svelte:document onclick={onDocumentClick} />

<div class="channel-sort" class:icon-only={iconOnly}>
    <button
        bind:this={trigger}
        class={iconOnly ? 'topbar-icon-btn' : 'channel-sort-trigger'}
        type="button"
        title="Sort"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={`Sort by ${label}`}
        onclick={toggle}
    >
        <ArrowDownUpIcon size={iconOnly ? 22 : 14} strokeWidth={2} aria-hidden="true" />
        {#if !iconOnly}<span>{label}</span>{/if}
    </button>
    {#if open}
        <div bind:this={menu} class="channel-sort-menu" role="menu" aria-label="Sort by" tabindex="-1" onkeydown={onMenuKeydown}>
            {#each CHANNEL_SORTS as option (option.value)}
                <button
                    class="channel-sort-option"
                    type="button"
                    role="menuitemradio"
                    aria-checked={value === option.value}
                    onclick={() => choose(option.value)}
                >
                    <span>{option.label}</span>
                    {#if value === option.value}<CheckIcon size={14} strokeWidth={2.5} aria-hidden="true" />{/if}
                </button>
            {/each}
        </div>
    {/if}
</div>

<style>
    .channel-sort {
        position: relative;
        flex: 0 0 auto;
    }

    /* Sits beside the segmented filter and borrows its shape, so the two read
       as one row of view controls. */
    .channel-sort-trigger {
        display: inline-flex;
        align-items: center;
        gap: 6px;
        height: 32px;
        padding: 0 12px;
        border: 0;
        border-radius: var(--radius-pill);
        background: var(--bg-panel);
        color: var(--text-muted);
        font: inherit;
        font-size: var(--type-xs);
        font-weight: var(--weight-semibold);
        cursor: pointer;
        transition: color var(--motion-fast) var(--ease-standard);
    }

    .channel-sort-trigger:hover,
    .channel-sort-trigger[aria-expanded='true'] { color: var(--text-main); }
    .channel-sort-trigger:focus-visible { outline: none; box-shadow: var(--focus-ring); }

    .channel-sort-menu {
        position: absolute;
        top: calc(100% + 6px);
        right: 0;
        z-index: var(--z-popover);
        min-width: 168px;
        padding: var(--space-1);
        border: 1px solid var(--border);
        border-radius: var(--radius-lg);
        background: var(--color-surface-1);
        box-shadow: var(--shadow-md);
        outline: none;
        animation: channel-sort-in var(--motion-fast) var(--ease-enter);
    }

    .channel-sort-option {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: var(--space-3);
        width: 100%;
        min-height: 32px;
        padding: 0 10px;
        border: 0;
        border-radius: var(--radius-sm);
        background: transparent;
        color: var(--text-main);
        font: inherit;
        font-size: var(--type-sm);
        text-align: left;
        cursor: pointer;
    }

    .channel-sort-option:hover,
    .channel-sort-option:focus-visible { outline: none; background: var(--overlay-neutral-2); }
    .channel-sort-option[aria-checked='true'] { color: var(--accent); }

    :global(html.mobile) .channel-sort-menu { right: 4px; min-width: 188px; }
    :global(html.mobile) .channel-sort-option { min-height: 44px; padding: 0 12px; font-size: 0.9375rem; }

    @keyframes channel-sort-in {
        from { opacity: 0; transform: translateY(-4px); }
    }

    @media (prefers-reduced-motion: reduce) {
        .channel-sort-trigger { transition: none; }
        .channel-sort-menu { animation: none; }
    }
</style>
