<script lang="ts">
    import ChevronLeftIcon from '@lucide/svelte/icons/chevron-left';
    import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
    import SearchIcon from '@lucide/svelte/icons/search';
    import EllipsisIcon from '@lucide/svelte/icons/ellipsis';
    import CheckIcon from '@lucide/svelte/icons/check';
    import { tick } from 'svelte';
    import { breadcrumbPath } from '../chrome/breadcrumb-store';
    import { fileSortState, setFileSortKey } from '../file-list/file-sort-store';
    import type { FileSortKey } from '../file-list/file-sort';
    import { navigateBack } from '../../modules/navigation';
    import { showPhotos } from '../../modules/gallery';
    import { appActions } from '../../modules/app-actions';
    import { clearSearch } from '../../modules/search';
    import { clearSelection, selectAllRows, startSelectionMode } from '../../modules/selection';
    import { albumsView, photosMode } from '../gallery/gallery-store';
    import { pushSheet } from '../modals/sheet-stack';
    import { selectionBarState } from '../selection/selection-bar-store';
    import { sidebarState } from '../sidebar/sidebar-store';
    import { askEmptyTrash, closeTrash, openTrash, trashEntries } from '../../modules/trash/controller';
    import SyncRing from './SyncRing.svelte';
    import {
        ACCOUNT_DETAIL_TITLES,
        accountDetail,
        activeDrive,
        activeTab,
        closeAccountDetail,
        ringState,
        fileListCount,
        openDriveSwitcher,
        type MobileTab,
    } from './mobile-shell-store';

    interface Props {
        active: MobileTab;
    }

    let { active }: Props = $props();

    // The search field costs a permanent 52px band on every screen, so it stays
    // collapsed behind its icon. It is hidden rather than unmounted: the
    // imperative search controller binds to #search-input once at startup and
    // that binding has to survive navigation.
    let searchOpen = $state(false);
    const inFolder = $derived($breadcrumbPath.length > 0);
    // The trash is a place the Files tab shows, and it needs its own bar: under
    // the drive header it had no name on screen, kept Search and Sort that do
    // not apply to it, and counted deleted items as the drive's files.
    const inTrash = $derived($sidebarState.virtualView === 'trash');
    const trashCountLabel = $derived(
        $trashEntries.length === 0 ? 'Empty'
        : $trashEntries.length === 1 ? '1 item'
        : `${$trashEntries.length} items`,
    );
    const folderName = $derived($breadcrumbPath[$breadcrumbPath.length - 1]?.name ?? '');
    const driveName = $derived($activeDrive?.title || 'Drive');
    const driveKind = $derived($activeDrive?.kind === 'shared' ? 'Shared' : 'Personal');
    const countLabel = $derived(
        $fileListCount === 0 ? 'No files'
        : $fileListCount === 1 ? '1 file'
        : `${$fileListCount} files`,
    );
    // A drive titled the same as its kind ("Personal") would read "Personal ·
    // Personal · 16 files" with the name above it, so where the two are the same
    // word the kind is dropped and only the count remains.
    const kindRepeatsName = $derived(driveName.trim().toLowerCase() === driveKind.toLowerCase());
    // While the search field is open the rows are results, not the drive, so
    // the header says only what kind of drive this is rather than "No files".
    const driveMeta = $derived(
        searchOpen ? (kindRepeatsName ? '' : driveKind)
        : kindRepeatsName ? countLabel
        : `${driveKind} · ${countLabel}`,
    );
    // Inside a folder the row set mixes folders and files, so "items" is the
    // honest noun where the drive header can say "files".
    const itemsLabel = $derived(
        $fileListCount === 0 ? 'Empty'
        : $fileListCount === 1 ? '1 item'
        : `${$fileListCount} items`,
    );

    // Selecting takes the tab bar away and the selection bar keeps the bulk
    // actions, so the count and the way out belong here. An iPhone has no
    // hardware BACK: without a Done on screen there is no exit from the mode at
    // all short of deselecting every row one at a time.
    const selecting = $derived(Boolean($selectionBarState.active || $selectionBarState.count > 0));
    const selectionLabel = $derived(
        $selectionBarState.count === 1 ? '1 selected' : `${$selectionBarState.count} selected`,
    );

    // Photos: the grid, the timeline, or one album. An album is a drill-in, so
    // its name and a back button take the bar the way a folder's do; Select is
    // offered wherever there are photos to pick (the timeline or an album) and
    // withheld on the Albums grid, whose tiles are folders, not photos.
    const photosView = $derived($photosMode);
    const inAlbum = $derived(photosView.kind === 'album');
    const albumName = $derived(photosView.kind === 'album' ? photosView.tile.name : '');
    const albumTiles = $derived($albumsView.status === 'ready' ? $albumsView.tiles : []);
    // The grid's live figure for this folder, not the one the tile carried when
    // it was tapped, so a refresh that adds photos while it is open moves it.
    const albumCount = $derived(
        photosView.kind === 'album'
            ? (albumTiles.find((tile) => tile.folderId === photosView.tile.folderId)?.countLabel ?? photosView.tile.countLabel)
            : '',
    );
    const canSelectPhotos = $derived(photosView.kind !== 'albums');

    let sortOpen = $state(false);
    let sortMenuEl = $state<HTMLElement | null>(null);
    let sortButtonEl = $state<HTMLButtonElement | null>(null);
    let searchInputEl = $state<HTMLInputElement | null>(null);

    async function toggleSearch(): Promise<void> {
        if (searchOpen) {
            closeSearch();
            return;
        }
        searchOpen = true;
        await tick();
        searchInputEl?.focus();
    }

    // Putting the field away drops the query with it. A drive still filtered by
    // words the user can no longer see is the one outcome worse than losing
    // them: the list looks wrong and nothing on screen says why.
    function closeSearch(): void {
        searchOpen = false;
        if (String(searchInputEl?.value ?? '').trim()) clearSearch();
    }

    // Desktop sorts by clicking a column header and has no Type column, so
    // Type lives only here, where the sort menu is its own control.
    const sortOptions: Array<{ key: FileSortKey; label: string }> = [
        { key: 'name', label: 'Name' },
        { key: 'date', label: 'Date added' },
        { key: 'size', label: 'Size' },
        { key: 'type', label: 'Type' },
    ];

    // The toolbar says what the list is currently sorted by, so the order the
    // reader is looking at is never a mystery they have to open a menu to solve.
    const currentSortLabel = $derived(
        sortOptions.find((option) => option.key === $fileSortState.key)?.label ?? 'Name',
    );

    // The overflow menu carries the sort radios and, under them, the actions a
    // phone has nowhere else to reach: Select (multi-select starts on a long
    // press otherwise), Refresh (a pull otherwise) and the drive's Trash.
    const menuActions: Array<{ label: string; run: () => void }> = [
        { label: 'Select', run: () => { startSelectionMode(); closeSort(); } },
        { label: 'Refresh', run: () => { void appActions().triggerRefresh(); closeSort(); } },
        { label: 'Trash', run: () => { openTrash(); closeSort(); } },
    ];

    function menuItems(): HTMLButtonElement[] {
        return Array.from(sortMenuEl?.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"], [role="menuitem"]') ?? []);
    }

    function restoreSortFocus(): void {
        void tick().then(() => sortButtonEl?.focus({ preventScroll: true }));
    }

    function closeSort({ restoreFocus = false }: { restoreFocus?: boolean } = {}): void {
        if (!sortOpen) return;
        sortOpen = false;
        if (restoreFocus) restoreSortFocus();
    }

    async function toggleSort(): Promise<void> {
        if (sortOpen) {
            closeSort({ restoreFocus: true });
            return;
        }
        sortOpen = !sortOpen;
        await tick();
        const items = menuItems();
        const checkedIndex = sortOptions.findIndex((option) => option.key === $fileSortState.key);
        items[Math.max(checkedIndex, 0)]?.focus({ preventScroll: true });
    }

    function chooseSort(key: FileSortKey): void {
        setFileSortKey(key);
        closeSort({ restoreFocus: true });
    }

    function onWindowKeydown(event: KeyboardEvent): void {
        if (event.key === 'Escape' && sortOpen) {
            event.preventDefault();
            closeSort({ restoreFocus: true });
        }
    }

    function onSortMenuKeydown(event: KeyboardEvent): void {
        const items = menuItems();
        if (items.length === 0) return;
        const activeIndex = Math.max(items.indexOf(document.activeElement as HTMLButtonElement), 0);
        let targetIndex: number | null = null;
        switch (event.key) {
            case 'ArrowDown': targetIndex = (activeIndex + 1) % items.length; break;
            case 'ArrowUp': targetIndex = (activeIndex - 1 + items.length) % items.length; break;
            case 'Home': targetIndex = 0; break;
            case 'End': targetIndex = items.length - 1; break;
            case 'Escape':
                event.preventDefault();
                closeSort({ restoreFocus: true });
                return;
            case 'Enter':
            case ' ':
                // Each item owns what it does, radios and actions alike, so the
                // key just takes the item's own click path.
                event.preventDefault();
                (document.activeElement as HTMLElement | null)?.click();
                return;
            default: return;
        }
        event.preventDefault();
        items[targetIndex]?.focus({ preventScroll: true });
    }

    function onDocumentClick(event: MouseEvent): void {
        if (!sortOpen) return;
        const target = event.target as Node;
        if (sortButtonEl?.contains(target) || sortMenuEl?.contains(target)) return;
        closeSort();
    }

    // Android BACK unwinds the bar the way it was built up: the menu first,
    // then the search field, before the press reaches the page underneath. Each
    // claim releases in its effect's cleanup, so an unmount cannot strand it.
    $effect(() => {
        if (!sortOpen) return;
        const back = pushSheet(() => closeSort({ restoreFocus: true }));
        return () => back.release();
    });

    $effect(() => {
        if (!searchOpen) return;
        const back = pushSheet(() => closeSearch());
        return () => back.release();
    });
</script>

<svelte:window onkeydown={onWindowKeydown} />
<svelte:document onclick={onDocumentClick} />

<header class="mobile-topbar" data-tab={active}>
    <!-- Selecting replaces the bar's contents rather than sitting beside them:
         navigating mid-selection is not something to offer, and the count and
         Done are what the mode needs. -->
    <div class="topbar-context topbar-plain topbar-selection" hidden={!selecting}>
        <div class="topbar-row">
            <h1 class="topbar-title topbar-selection-count" aria-live="polite">{selectionLabel}</h1>
            <div class="topbar-actions">
                {#if active === 'files' && !inTrash}
                    <button type="button" class="topbar-selectall" onclick={() => selectAllRows()}>Select all</button>
                {/if}
                <button type="button" class="topbar-done" onclick={() => clearSelection()}>Done</button>
            </div>
        </div>
    </div>

    <!-- Files: drive header at the root, a back chevron and folder name inside a
         folder. The search field stays mounted across tabs so its controller
         binding survives navigation. -->
    <div class="topbar-context topbar-plain topbar-trash" hidden={selecting || active !== 'files' || !inTrash}>
        <div class="topbar-row">
            <button type="button" class="topbar-back" aria-label="Back" onclick={() => closeTrash()}>
                <ChevronLeftIcon size={24} strokeWidth={2} aria-hidden="true" />
            </button>
            <div class="topbar-plain-titles">
                <h1 class="topbar-title">Trash</h1>
                <span class="topbar-subtitle">{trashCountLabel}</span>
            </div>
            {#if $trashEntries.length > 0}
                <div class="topbar-actions">
                    <button type="button" class="topbar-done topbar-danger" onclick={() => askEmptyTrash()}>Empty</button>
                </div>
            {/if}
        </div>
    </div>

    <div class="topbar-context topbar-files" hidden={selecting || active !== 'files' || inTrash}>
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
                <!-- The ring is the queue's own button, so it is a sibling of the
                     switcher's, not a child of it: nested, one tap ran both and
                     opened the switcher over the Transfers tab. -->
                <div class="drive-header">
                    <div class="drive-header-main">
                        <button
                            type="button"
                            class="drive-header-btn"
                            aria-haspopup="dialog"
                            aria-label={`${driveName}, ${driveKind} drive, ${countLabel}. Switch drive`}
                            onclick={openDriveSwitcher}
                        >
                            <span class="drive-header-name" title={driveName}>{driveName}</span>
                            <ChevronDownIcon class="drive-header-chevron" size={16} strokeWidth={2.4} aria-hidden="true" />
                        </button>
                        <SyncRing status={$ringState} onOpenQueue={() => activeTab.set('transfers')} />
                    </div>
                    <span class="drive-header-meta">{driveMeta}</span>
                </div>
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
                        aria-controls="mobile-sort-menu"
                        aria-label={`Sort and more. Sorted by ${currentSortLabel}, ${$fileSortState.direction === 'asc' ? 'ascending' : 'descending'}.`}
                        onclick={toggleSort}
                    >
                        <EllipsisIcon size={22} strokeWidth={2} aria-hidden="true" />
                    </button>
                    {#if sortOpen}
                        <div
                            id="mobile-sort-menu"
                            bind:this={sortMenuEl}
                            class="topbar-menu"
                            role="menu"
                            aria-label="File options"
                            aria-orientation="vertical"
                            tabindex="-1"
                            onkeydown={onSortMenuKeydown}
                        >
                            <div class="topbar-menu-label" role="presentation" aria-hidden="true">Sort by</div>
                            {#each sortOptions as option (option.key)}
                                <button
                                    type="button"
                                    class="topbar-menu-item"
                                    role="menuitemradio"
                                    aria-checked={$fileSortState.key === option.key}
                                    data-sort-key={option.key}
                                    onclick={() => chooseSort(option.key)}
                                >
                                    <span>{option.label}</span>
                                    {#if $fileSortState.key === option.key}
                                        <CheckIcon size={16} strokeWidth={2.5} aria-hidden="true" />
                                    {/if}
                                </button>
                            {/each}
                            <div class="topbar-menu-sep" role="separator"></div>
                            {#each menuActions as action (action.label)}
                                <button
                                    type="button"
                                    class="topbar-menu-item"
                                    role="menuitem"
                                    onclick={action.run}
                                >
                                    <span>{action.label}</span>
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
                type="search"
                enterkeyhint="search"
                placeholder="Search this drive"
                autocomplete="off"
                autocapitalize="off"
                spellcheck="false"
                aria-label="Search files"
            />
        </div>
        </div>
    </div>

    <!-- Photos: the same drive, gallery view. Inside an album the bar carries
         the album's name and a way back to the grid, like a folder does. -->
    <div class="topbar-context topbar-plain topbar-photos" hidden={selecting || active !== 'photos'}>
        <div class="topbar-row">
            {#if inAlbum}
                <button type="button" class="topbar-back" aria-label="Back to albums" onclick={() => void showPhotos({ kind: 'albums' })}>
                    <ChevronLeftIcon size={24} strokeWidth={2} aria-hidden="true" />
                </button>
                <div class="topbar-plain-titles">
                    <h1 class="topbar-title" title={albumName}>{albumName}</h1>
                    {#if albumCount}<span class="topbar-subtitle">{albumCount}</span>{/if}
                </div>
            {:else}
                <div class="topbar-plain-titles">
                    <h1 class="topbar-title">Photos</h1>
                    <span class="topbar-subtitle">{driveName}</span>
                </div>
            {/if}
            {#if canSelectPhotos}
                <div class="topbar-actions">
                    <button
                        type="button"
                        class="topbar-done"
                        aria-label="Select photos"
                        onclick={startSelectionMode}
                    >Select</button>
                </div>
            {/if}
        </div>
    </div>

    <div class="topbar-context topbar-plain" hidden={selecting || active !== 'transfers'}>
        <div class="topbar-row">
            <h1 class="topbar-title">Transfers</h1>
        </div>
    </div>

    <!-- Account: a settings page opened from the list carries its own title and
         a way back here, the way a folder and the trash do. -->
    <div class="topbar-context topbar-plain" hidden={selecting || active !== 'account'}>
        <div class="topbar-row">
            {#if $accountDetail}
                <button type="button" class="topbar-back" aria-label="Back" onclick={() => closeAccountDetail()}>
                    <ChevronLeftIcon size={24} strokeWidth={2} aria-hidden="true" />
                </button>
                <div class="topbar-plain-titles">
                    <h1 class="topbar-title">{ACCOUNT_DETAIL_TITLES[$accountDetail]}</h1>
                </div>
            {:else}
                <h1 class="topbar-title">Account</h1>
            {/if}
        </div>
    </div>
</header>
