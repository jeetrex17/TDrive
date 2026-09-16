<script lang="ts">
    import ChevronLeftIcon from '@lucide/svelte/icons/chevron-left';
    import SearchIcon from '@lucide/svelte/icons/search';
    import EllipsisIcon from '@lucide/svelte/icons/ellipsis';
    import CheckIcon from '@lucide/svelte/icons/check';
    import { tick } from 'svelte';
    import { breadcrumbPath } from '../chrome/breadcrumb-store';
    import { fileSortState, setFileSortKey } from '../file-list/file-sort-store';
    import type { FileSortKey } from '../file-list/file-sort';
    import { navigateBack } from '../../modules/navigation';
    import SyncRing from './SyncRing.svelte';
    import {
        activeDrive,
        driveSyncStatus,
        fileListCount,
        openDriveSwitcher,
        type MobileTab,
    } from './mobile-shell-store';

    interface Props {
        active: MobileTab;
    }

    let { active }: Props = $props();

    const inFolder = $derived($breadcrumbPath.length > 0);
    const folderName = $derived($breadcrumbPath[$breadcrumbPath.length - 1]?.name ?? '');
    const driveName = $derived($activeDrive?.title || 'Drive');
    const driveKind = $derived($activeDrive?.kind === 'shared' ? 'Shared' : 'Personal');
    const countLabel = $derived(
        $fileListCount === 0 ? 'No files'
        : $fileListCount === 1 ? '1 file'
        : `${$fileListCount} files`,
    );
    // Inside a folder the row set mixes folders and files, so "items" is the
    // honest noun where the drive header can say "files".
    const itemsLabel = $derived(
        $fileListCount === 0 ? 'Empty'
        : $fileListCount === 1 ? '1 item'
        : `${$fileListCount} items`,
    );

    let sortOpen = $state(false);
    let sortMenuEl = $state<HTMLElement | null>(null);
    let sortButtonEl = $state<HTMLButtonElement | null>(null);
    // The search field costs a permanent 52px band on every screen, so it stays
    // collapsed behind its icon. It is hidden rather than unmounted: the
    // imperative search controller binds to #search-input once at startup and
    // that binding has to survive navigation.
    let searchOpen = $state(false);
    let searchInputEl = $state<HTMLInputElement | null>(null);

    async function toggleSearch(): Promise<void> {
        searchOpen = !searchOpen;
        if (!searchOpen) return;
        await tick();
        searchInputEl?.focus();
    }

    const sortOptions: Array<{ key: FileSortKey; label: string }> = [
        { key: 'name', label: 'Name' },
        { key: 'date', label: 'Date added' },
        { key: 'size', label: 'Size' },
    ];

    async function toggleSort(): Promise<void> {
        sortOpen = !sortOpen;
        if (!sortOpen) return;
        await tick();
        sortMenuEl?.querySelector<HTMLElement>('[role="menuitemradio"]')?.focus();
    }

    function chooseSort(key: FileSortKey): void {
        setFileSortKey(key);
        sortOpen = false;
        sortButtonEl?.focus();
    }

    function onWindowKeydown(event: KeyboardEvent): void {
        if (event.key === 'Escape' && sortOpen) {
            sortOpen = false;
            sortButtonEl?.focus();
        }
    }

    function onDocumentClick(event: MouseEvent): void {
        if (!sortOpen) return;
        const target = event.target as Node;
        if (sortButtonEl?.contains(target) || sortMenuEl?.contains(target)) return;
        sortOpen = false;
    }
</script>

<svelte:window onkeydown={onWindowKeydown} />
<svelte:document onclick={onDocumentClick} />

<header class="mobile-topbar" data-tab={active}>
    <!-- Files: drive header at the root, a back chevron and folder name inside a
         folder. The search field stays mounted across tabs so its controller
         binding survives navigation. -->
    <div class="topbar-context topbar-files" hidden={active !== 'files'}>
        <div class="topbar-row">
            {#if inFolder}
                <button type="button" class="topbar-back" aria-label="Back" onclick={() => navigateBack()}>
                    <ChevronLeftIcon size={24} strokeWidth={2} aria-hidden="true" />
                </button>
                <span class="topbar-folder-titles">
                    <h1 class="topbar-folder-title" title={folderName}>{folderName}</h1>
                    <span class="topbar-folder-meta">{itemsLabel}</span>
                </span>
            {:else}
                <button
                    type="button"
                    class="drive-header"
                    aria-haspopup="dialog"
                    aria-label={`${driveName}, ${driveKind} drive, ${countLabel}. Switch drive`}
                    onclick={openDriveSwitcher}
                >
                    <span class="drive-header-main">
                        <span class="drive-header-name" title={driveName}>{driveName}</span>
                        <SyncRing status={$driveSyncStatus} />
                    </span>
                    <span class="drive-header-meta">{driveKind} · {countLabel}</span>
                </button>
            {/if}

            <div class="topbar-actions">
                <button
                    type="button"
                    class="topbar-icon-btn"
                    aria-expanded={searchOpen}
                    aria-controls="search-input"
                    aria-label={searchOpen ? 'Hide search' : 'Search files'}
                    onclick={toggleSearch}
                >
                    <SearchIcon size={22} strokeWidth={2} aria-hidden="true" />
                </button>
                <div class="topbar-menu-wrap">
                    <button
                        bind:this={sortButtonEl}
                        type="button"
                        class="topbar-icon-btn"
                        aria-haspopup="menu"
                        aria-expanded={sortOpen}
                        aria-label="Sort and more"
                        onclick={toggleSort}
                    >
                        <EllipsisIcon size={22} strokeWidth={2} aria-hidden="true" />
                    </button>
                    {#if sortOpen}
                        <div bind:this={sortMenuEl} class="topbar-menu" role="menu" aria-label="Sort by">
                            <div class="topbar-menu-label">Sort by</div>
                            {#each sortOptions as option (option.key)}
                                <button
                                    type="button"
                                    class="topbar-menu-item"
                                    role="menuitemradio"
                                    aria-checked={$fileSortState.key === option.key}
                                    onclick={() => chooseSort(option.key)}
                                >
                                    <span>{option.label}</span>
                                    {#if $fileSortState.key === option.key}
                                        <CheckIcon size={16} strokeWidth={2.4} aria-hidden="true" />
                                    {/if}
                                </button>
                            {/each}
                        </div>
                    {/if}
                </div>
            </div>
        </div>

        <!-- Collapsed by height rather than `hidden`, so opening it is a
             movement instead of a jump. `inert` keeps the clipped field out of
             the tab order and the a11y tree while it is closed, without taking
             it out of the DOM, which the search controller's binding needs. -->
        <div class="topbar-search-shell" data-open={searchOpen} inert={!searchOpen}>
        <div class="topbar-search">
            <SearchIcon class="topbar-search-icon" size={18} strokeWidth={2} aria-hidden="true" />
            <input
                bind:this={searchInputEl}
                id="search-input"
                type="text"
                placeholder="Search this drive"
                autocomplete="off"
                autocapitalize="off"
                spellcheck="false"
                aria-label="Search files"
            />
        </div>
        </div>
    </div>

    <!-- Photos: the same drive, gallery view. -->
    <div class="topbar-context topbar-plain" hidden={active !== 'photos'}>
        <div class="topbar-row">
            <div class="topbar-plain-titles">
                <h1 class="topbar-title">Photos</h1>
                <span class="topbar-subtitle">{driveName}</span>
            </div>
        </div>
    </div>

    <div class="topbar-context topbar-plain" hidden={active !== 'transfers'}>
        <div class="topbar-row">
            <h1 class="topbar-title">Transfers</h1>
        </div>
    </div>

    <div class="topbar-context topbar-plain" hidden={active !== 'account'}>
        <div class="topbar-row">
            <h1 class="topbar-title">Account</h1>
        </div>
    </div>
</header>
