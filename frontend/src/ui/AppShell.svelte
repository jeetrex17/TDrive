<script lang="ts">
    import ArrowDownIcon from '@lucide/svelte/icons/arrow-down';
    import ArrowUpIcon from '@lucide/svelte/icons/arrow-up';
    import FolderPlusIcon from '@lucide/svelte/icons/folder-plus';
    import ImagesIcon from '@lucide/svelte/icons/images';
    import Link2Icon from '@lucide/svelte/icons/link-2';
    import SearchIcon from '@lucide/svelte/icons/search';
    import { isMobilePlatform, listMountableDrives } from '../api';
    import tdriveLogo from '../assets/images/tdrive-logo.png';
    import { setFileSortKey, fileSortState } from './file-list/file-sort-store';
    import type { FileSortKey } from './file-list/file-sort';
    import MountControl from './mount/MountControl.svelte';
    import FeatureLayer from './app/FeatureLayer.svelte';

    interface Props {
        dashboardVisible: boolean;
    }

    let { dashboardVisible }: Props = $props();

    // OS mounts are desktop-only; the phone builds ship no WebDAV connector.
    const mountAvailable = !isMobilePlatform();

    function sortButtonLabel(key: FileSortKey): string {
        const active = $fileSortState.key === key;
        if (!active) return `Sort by ${key}`;
        return `Sort by ${key} ${$fileSortState.direction === 'asc' ? 'descending' : 'ascending'}`;
    }

    function sortAriaValue(key: FileSortKey): 'ascending' | 'descending' | 'none' {
        if ($fileSortState.key !== key) return 'none';
        return $fileSortState.direction === 'asc' ? 'ascending' : 'descending';
    }

</script>

{#snippet sortIndicator(key: FileSortKey)}
    {#if $fileSortState.key === key}
        {#if $fileSortState.direction === 'asc'}
            <ArrowUpIcon class="sort-direction-up" size={12} strokeWidth={2.5} aria-hidden="true" />
        {:else}
            <ArrowDownIcon class="sort-direction-down" size={12} strokeWidth={2.5} aria-hidden="true" />
        {/if}
    {/if}
{/snippet}

<!-- Semantic dashboard chrome remains mounted after runtime startup. The typed
     root owns visibility; FeatureLayer owns persistent interactive surfaces. -->
<div
    id="success-screen"
    class="dashboard-container"
    hidden={!dashboardVisible}
    aria-hidden={dashboardVisible ? undefined : 'true'}
>
    <aside class="sidebar">
        <div class="logo">
            <img class="logo-mark" src={tdriveLogo} alt="" width="44" height="28" />
            <span>TDrive</span>
        </div>

        <nav id="drives-nav" class="drives-nav" tabindex="-1" aria-label="Drives">
            <div class="drives-scroll">
                <div class="drives-section">
                    <div class="drives-section-title">My Drive</div>
                    <div id="drives-personal" class="drives-list"></div>
                    <div class="drives-list">
                        <button id="nav-photos" class="drive-item nav-photos-item" type="button" title="Photos">
                            <ImagesIcon class="icon" size={18} strokeWidth={2} aria-hidden="true" />
                            <span class="drive-item-title">Photos</span>
                        </button>
                    </div>
                </div>

                <div class="drives-section">
                    <div class="drives-section-title">Shared with me</div>
                    <div id="drives-shared" class="drives-list"></div>
                </div>
            </div>

            <div class="drives-actions">
                <button id="open-new-drive" class="drive-action-btn" type="button" title="Create a new shared drive">
                    <FolderPlusIcon class="icon" size={16} strokeWidth={2} aria-hidden="true" />
                    New shared drive
                </button>
                <button id="open-join-drive" class="drive-action-btn" type="button" title="Join a shared drive via invite link">
                    <Link2Icon class="icon" size={16} strokeWidth={2} aria-hidden="true" />
                    Join with link
                </button>
                {#if dashboardVisible && mountAvailable}
                    <MountControl variant="sidebar" loadDrives={listMountableDrives} />
                {/if}
            </div>
        </nav>

        <div class="storage-info">
            <p>Storage:<span id="storage-used">0 B / Unlimited</span></p>
        </div>
    </aside>

    <main class="main-content">
        <header>
            <div class="search-bar">
                <SearchIcon class="search-icon" size={16} strokeWidth={2} aria-hidden="true" />
                <input id="search-input" type="text" placeholder="Search files..." autocomplete="off" spellcheck="false" aria-label="Search files">
            </div>

            <div class="header-actions">
                <div id="notif-bell-root" style="display: contents;"></div>

                <div class="upload-menu-wrap" id="upload-menu-root"></div>
                <div id="profile-root" style="display: contents;"></div>
            </div>
        </header>

        <div class="drive-breadcrumb">
            <div id="breadcrumb-root" style="display: contents;"></div>
            <div id="gallery-title" class="gallery-title">Photos</div>
        </div>

        <div class="file-table-header" role="row" aria-rowindex="1">
            <div class="col-name" role="columnheader" aria-colindex="1" aria-sort={sortAriaValue('name')}>
                <button
                    class:active={$fileSortState.key === 'name'}
                    class="file-sort-button"
                    type="button"
                    aria-label={sortButtonLabel('name')}
                    onclick={() => setFileSortKey('name')}
                >
                    <span>Name</span>
                    <span class="file-sort-indicator" aria-hidden="true">{@render sortIndicator('name')}</span>
                </button>
            </div>
            <div class="col-date" role="columnheader" aria-colindex="2" aria-sort={sortAriaValue('date')}>
                <button
                    class:active={$fileSortState.key === 'date'}
                    class="file-sort-button"
                    type="button"
                    aria-label={sortButtonLabel('date')}
                    onclick={() => setFileSortKey('date')}
                >
                    <span>Date</span>
                    <span class="file-sort-indicator" aria-hidden="true">{@render sortIndicator('date')}</span>
                </button>
            </div>
            <div class="col-size" role="columnheader" aria-colindex="3" aria-sort={sortAriaValue('size')}>
                <button
                    class:active={$fileSortState.key === 'size'}
                    class="file-sort-button"
                    type="button"
                    aria-label={sortButtonLabel('size')}
                    onclick={() => setFileSortKey('size')}
                >
                    <span>Size</span>
                    <span class="file-sort-indicator" aria-hidden="true">{@render sortIndicator('size')}</span>
                </button>
            </div>
            <div class="col-actions" role="columnheader" aria-colindex="4">
                <span>Actions</span>
                <div id="selection-bar" class="selection-bar" style="display: none;" role="status" aria-live="polite"></div>
            </div>
        </div>

        <div id="file-list" class="file-list-box" data-file-drop-target role="grid" aria-label="Files" aria-multiselectable="true" aria-colcount="4"></div>

        <div id="gallery-view" class="gallery-view" tabindex="-1" aria-label="Photos"></div>
    </main>
</div>

<FeatureLayer />
