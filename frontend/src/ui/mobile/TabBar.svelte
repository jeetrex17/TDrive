<script lang="ts">
    import FolderIcon from '@lucide/svelte/icons/folder';
    import ImagesIcon from '@lucide/svelte/icons/images';
    import ArrowDownUpIcon from '@lucide/svelte/icons/arrow-down-up';
    import CircleUserIcon from '@lucide/svelte/icons/circle-user';
    import type { Component } from 'svelte';
    import Avatar from '../chrome/Avatar.svelte';
    import { profileLoaded, profileUser } from '../chrome/profile-store';
    import Fab from './Fab.svelte';
    import type { MobileTab } from './mobile-shell-store';

    interface Props {
        active: MobileTab;
        /**
         * How many transfers need a person -- not how many are running. A badge
         * is an interrupt, so it only appears when there is something to act on.
         * Progress is ambient and belongs to the header ring.
         */
        transferBadge: number;
        onSelect: (tab: MobileTab) => void;
    }

    let { active, transferBadge, onSelect }: Props = $props();

    // Past nine the exact number stops changing the decision -- the user is
    // going to open the queue either way -- so the badge stops counting and
    // stays a fixed width instead of pushing the icon around.
    const BADGE_CAP = 9;

    const tabs: Array<{ id: MobileTab; label: string; icon: Component }> = [
        { id: 'files', label: 'Files', icon: FolderIcon },
        { id: 'photos', label: 'Photos', icon: ImagesIcon },
        { id: 'transfers', label: 'Transfers', icon: ArrowDownUpIcon },
        { id: 'account', label: 'Account', icon: CircleUserIcon },
    ];

    // The upload button sits in the middle of the bar, so the destinations
    // split two and two around it.
    const DOCK_AFTER = 2;

    // The account tab is the one that is about the person using the app, so it
    // shows them rather than a drawing of a person. The icon stays until the
    // profile arrives: an empty disc in the meantime would read as a fault
    // rather than as loading.
    const showAvatar = $derived($profileLoaded && $profileUser !== null);

    function badgeLabel(tab: MobileTab): string {
        if (tab !== 'transfers' || transferBadge <= 0) return '';
        return transferBadge === 1 ? ', 1 transfer needs attention' : `, ${transferBadge} transfers need attention`;
    }
</script>

<nav class="tab-bar" aria-label="Primary">
    {#each tabs as tab, index (tab.id)}
        {#if index === DOCK_AFTER}
            <!-- The upload action is a cell of this row, not something floated
                 over it. Sharing the row's own sizing is what keeps all five
                 centres evenly spaced; parked on top with a fixed width it made
                 the two middle gaps narrower than the two outer ones.
                 It is still not a tab stop, so the destinations stay a clean
                 four for a screen reader. -->
            <Fab />
        {/if}
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
                <!-- The pill grows behind the icon rather than cutting in, so a
                     tab change reads as one object settling (motion-17). -->
                <span class="tab-pill" aria-hidden="true"></span>
                {#if tab.id === 'account' && showAvatar}
                    <Avatar user={$profileUser} />
                {:else}
                    <Icon size={24} strokeWidth={active === tab.id ? 2.2 : 1.9} aria-hidden="true" />
                {/if}
                {#if tab.id === 'transfers' && transferBadge > 0}
                    <span class="tab-badge" aria-hidden="true">{transferBadge > BADGE_CAP ? `${BADGE_CAP}+` : transferBadge}</span>
                {/if}
            </span>
            <span class="tab-label">{tab.label}</span>
        </button>
    {/each}
</nav>
