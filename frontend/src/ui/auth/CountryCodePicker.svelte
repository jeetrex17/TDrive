<script lang="ts">
    import type { CountryCode } from 'libphonenumber-js/min';
    import ChevronDown from '@lucide/svelte/icons/chevron-down';
    import Check from '@lucide/svelte/icons/check';
    import Search from '@lucide/svelte/icons/search';
    import ModalShell from '../modals/ModalShell.svelte';
    import { portal } from '../notifications/portal';
    import { countries } from './phone-number';

    interface Props {
        country: CountryCode | undefined;
        disabled?: boolean;
        onSelect: (country: CountryCode) => void;
    }

    let { country, disabled = false, onSelect }: Props = $props();
    let open = $state(false);
    let query = $state('');
    const selected = $derived(countries.find((item) => item.code === country));
    const visible = $derived.by(() => {
        const needle = query.trim().toLocaleLowerCase();
        if (!needle) return countries;
        return countries.filter((item) => item.name.toLocaleLowerCase().includes(needle)
            || item.code.toLowerCase() === needle
            || `+${item.callingCode}`.startsWith(needle.startsWith('+') ? needle : `+${needle}`));
    });

    function show(): void {
        if (disabled) return;
        query = '';
        open = true;
    }

    function choose(code: CountryCode): void {
        if (disabled) return;
        open = false;
        onSelect(code);
    }

    function searchKeydown(event: KeyboardEvent): void {
        if (event.key !== 'Enter' || event.isComposing) return;
        event.preventDefault();
        if (visible.length === 1) choose(visible[0].code);
    }

    $effect(() => {
        if (disabled) open = false;
    });
</script>

<button
    id="telegram-country"
    type="button"
    class="country-code-trigger"
    {disabled}
    aria-label={selected ? `Country code: ${selected.name} +${selected.callingCode}` : 'Choose country code'}
    aria-haspopup="dialog"
    aria-expanded={open}
    aria-controls="country-code-modal"
    onclick={show}
>
    {#if selected}
        <span aria-hidden="true">{selected.flag}</span>
        <span>+{selected.callingCode}</span>
    {:else}
        <span>Country</span>
    {/if}
    <ChevronDown size={16} aria-hidden="true" />
</button>

<div id="country-code-modal" use:portal>
    <ModalShell
        hostId="country-code-modal"
        {open}
        title="Choose country"
        titleId="country-code-title"
        cardClass="country-code-card"
        initialFocus="#country-code-search"
        restoreFocus="#telegram-country"
        onClose={() => { open = false; }}
    >
        <label class="country-code-search">
            <Search size={18} aria-hidden="true" />
            <input
                id="country-code-search"
                type="search"
                bind:value={query}
                aria-label="Search country or calling code"
                aria-controls="country-code-results"
                placeholder="Country or calling code"
                autocomplete="off"
                spellcheck="false"
                onkeydown={searchKeydown}
            />
        </label>
        <div id="country-code-results" class="country-code-results">
            {#each visible as item (item.code)}
                <button
                    type="button"
                    class="country-code-option"
                    class:selected={country === item.code}
                    aria-label={`${item.name} +${item.callingCode}`}
                    aria-pressed={country === item.code}
                    onclick={() => choose(item.code)}
                >
                    <span class="country-flag" aria-hidden="true">{item.flag}</span>
                    <span class="country-name">{item.name}</span>
                    <span class="country-dial-code">+{item.callingCode}</span>
                    <span class="country-check" aria-hidden="true">
                        {#if country === item.code}<Check size={16} />{/if}
                    </span>
                </button>
            {:else}
                <p class="country-code-empty" role="status">No countries found. Try a country name or calling code.</p>
            {/each}
        </div>
        {#snippet actions()}
            <button type="button" class="secondary-btn" onclick={() => { open = false; }}>Cancel</button>
        {/snippet}
    </ModalShell>
</div>

<style>
    .country-code-trigger {
        display: flex;
        align-items: center;
        justify-content: center;
        gap: var(--space-2);
        width: 100%;
        min-height: 48px;
        padding: var(--space-3);
        border: 1px solid var(--border);
        border-radius: var(--radius-md);
        background: var(--bg-dark);
        color: var(--text-main);
        font: inherit;
        white-space: nowrap;
        cursor: pointer;
    }
    .country-code-trigger:disabled { opacity: 0.6; cursor: default; }
    .country-code-trigger:hover:not(:disabled), .country-code-option:hover { background: var(--bg-panel); }
    .country-code-trigger:focus-visible, .country-code-option:focus-visible {
        outline: 2px solid var(--accent);
        outline-offset: 2px;
    }
    :global(.country-code-card) { width: min(440px, 100%); }
    .country-code-search {
        display: flex;
        align-items: center;
        gap: var(--space-2);
        min-height: 48px;
        padding: 0 var(--space-3);
        border: 1px solid var(--border);
        border-radius: var(--radius-md);
        background: var(--bg-dark);
        color: var(--text-muted);
    }
    .country-code-search:focus-within { outline: 2px solid var(--accent); outline-offset: 2px; }
    .country-code-search input {
        flex: 1;
        min-width: 0;
        width: 100%;
        margin: 0;
        padding: var(--space-3) 0;
        border: 0;
        background: transparent;
        color: var(--text-main);
        font: inherit;
        font-size: max(16px, var(--type-base));
        outline: none;
        box-shadow: none;
    }
    .country-code-results {
        max-height: min(360px, 45dvh);
        overflow-y: auto;
        overscroll-behavior: contain;
        margin: var(--space-3) calc(-1 * var(--space-1));
        padding: var(--space-1);
    }
    .country-code-option {
        display: flex;
        align-items: center;
        gap: var(--space-3);
        width: 100%;
        min-height: 48px;
        padding: var(--space-2);
        border: 0;
        border-radius: var(--radius-md);
        background: transparent;
        color: var(--text-main);
        font: inherit;
        text-align: left;
        cursor: pointer;
    }
    .country-code-option.selected { background: var(--bg-panel); }
    .country-flag { flex: 0 0 24px; }
    .country-name { flex: 1; min-width: 0; overflow-wrap: anywhere; }
    .country-dial-code { color: var(--text-muted); font-variant-numeric: tabular-nums; }
    .country-check { display: flex; flex: 0 0 16px; color: var(--accent); }
    .country-code-empty { padding: var(--space-4); color: var(--text-muted); }
</style>
