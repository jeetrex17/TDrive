<script lang="ts">
    import FolderIcon from '@lucide/svelte/icons/folder';
    import ImagesIcon from '@lucide/svelte/icons/images';
    import ArrowDownUpIcon from '@lucide/svelte/icons/arrow-down-up';
    import CircleUserIcon from '@lucide/svelte/icons/circle-user';
    import type { Component } from 'svelte';
    import type { MobileTab } from './mobile-shell-store';

    interface Props {
        active: MobileTab;
        transferBadge: number;
        onSelect: (tab: MobileTab) => void;
    }

    let { active, transferBadge, onSelect }: Props = $props();

    const tabs: Array<{ id: MobileTab; label: string; icon: Component }> = [
        { id: 'files', label: 'Files', icon: FolderIcon },
        { id: 'photos', label: 'Photos', icon: ImagesIcon },
        { id: 'transfers', label: 'Transfers', icon: ArrowDownUpIcon },
        { id: 'account', label: 'Account', icon: CircleUserIcon },
    ];

    const activeIndex = $derived(Math.max(0, tabs.findIndex((tab) => tab.id === active)));

    function badgeLabel(tab: MobileTab): string {
        if (tab !== 'transfers' || transferBadge <= 0) return '';
        return transferBadge === 1 ? ', 1 active transfer' : `, ${transferBadge} active transfers`;
    }
</script>

<nav class="tab-bar" aria-label="Primary">
    <!-- One indicator slides between destinations so the change reads as a
         single object moving, not a hard recolour (motion-17). -->
    <span
        class="tab-indicator"
        style={`transform: translateX(${activeIndex * 100}%)`}
        aria-hidden="true"
    ></span>
    {#each tabs as tab (tab.id)}
        {@const Icon = tab.icon}
        <button
            type="button"
            class="tab-item"
            class:active={active === tab.id}
            aria-current={active === tab.id ? 'page' : undefined}
            aria-label={`${tab.label}${badgeLabel(tab.id)}`}
            onclick={() => onSelect(tab.id)}
        >
            <span class="tab-icon">
                <Icon size={24} strokeWidth={active === tab.id ? 2.2 : 1.9} aria-hidden="true" />
                {#if tab.id === 'transfers' && transferBadge > 0}
                    <span class="tab-badge" aria-hidden="true">{transferBadge > 9 ? '9+' : transferBadge}</span>
                {/if}
            </span>
            <span class="tab-label">{tab.label}</span>
        </button>
    {/each}
</nav>
