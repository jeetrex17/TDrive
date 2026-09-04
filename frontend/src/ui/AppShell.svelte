<script lang="ts">
    import ArrowDownIcon from '@lucide/svelte/icons/arrow-down';
    import ArrowUpIcon from '@lucide/svelte/icons/arrow-up';
    import CloudUploadIcon from '@lucide/svelte/icons/cloud-upload';
    import FolderPlusIcon from '@lucide/svelte/icons/folder-plus';
    import ImagesIcon from '@lucide/svelte/icons/images';
    import Link2Icon from '@lucide/svelte/icons/link-2';
    import SearchIcon from '@lucide/svelte/icons/search';
    import { listMountableDrives } from '../api';
    import { setFileSortKey, fileSortState } from './file-list/file-sort-store';
    import type { FileSortKey } from './file-list/file-sort';
    import MountControl from './mount/MountControl.svelte';

    function sortButtonLabel(key: FileSortKey): string {
        const active = $fileSortState.key === key;
        if (!active) return `Sort by ${key}`;
        return `Sort by ${key} ${$fileSortState.direction === 'asc' ? 'descending' : 'ascending'}`;
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

<!-- Application chrome and stable mount hosts. Runtime behavior stays in the
     TypeScript modules that mount into these IDs. -->
<div id="auth-wrapper"></div>

<div id="success-screen" class="dashboard-container" style="display: none;">
    <aside class="sidebar">
        <div class="logo">TDrive</div>

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
                <MountControl variant="sidebar" loadDrives={listMountableDrives} />
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

        <div class="file-table-header">
            <button
                class:active={$fileSortState.key === 'name'}
                class="file-sort-button col-name"
                type="button"
                aria-label={sortButtonLabel('name')}
                aria-pressed={$fileSortState.key === 'name'}
                onclick={() => setFileSortKey('name')}
            >
                <span>Name</span>
                <span class="file-sort-indicator" aria-hidden="true">{@render sortIndicator('name')}</span>
            </button>
            <button
                class:active={$fileSortState.key === 'date'}
                class="file-sort-button col-date"
                type="button"
                aria-label={sortButtonLabel('date')}
                aria-pressed={$fileSortState.key === 'date'}
                onclick={() => setFileSortKey('date')}
            >
                <span>Date</span>
                <span class="file-sort-indicator" aria-hidden="true">{@render sortIndicator('date')}</span>
            </button>
            <button
                class:active={$fileSortState.key === 'size'}
                class="file-sort-button col-size"
                type="button"
                aria-label={sortButtonLabel('size')}
                aria-pressed={$fileSortState.key === 'size'}
                onclick={() => setFileSortKey('size')}
            >
                <span>Size</span>
                <span class="file-sort-indicator" aria-hidden="true">{@render sortIndicator('size')}</span>
            </button>
            <span class="col-actions">Actions</span>
            <div id="selection-bar" class="selection-bar" style="display: none;" role="status" aria-live="polite"></div>
        </div>

        <div id="file-list" class="file-list-box" role="listbox" tabindex="0" aria-label="Files" aria-multiselectable="true">
            <div class="empty-state">Loading...</div>
        </div>

        <div id="gallery-view" class="gallery-view" tabindex="-1" aria-label="Photos"></div>
    </main>
</div>

<div id="drop-overlay" class="drop-overlay" aria-hidden="true" hidden>
    <div class="drop-overlay-card">
        <CloudUploadIcon size={30} aria-hidden="true" />
        <strong id="drop-overlay-title">Drop to add here</strong>
        <span>Files and folders go into the open folder</span>
    </div>
</div>

<div id="context-menu" class="context-menu"></div>

<div id="folder-modal" class="modal-overlay" style="display: none;"></div>
<div id="delete-modal" class="modal-overlay" style="display: none;"></div>
<div id="rename-modal" class="modal-overlay" style="display: none;"></div>
<div id="move-modal" class="modal-overlay" style="display: none;"></div>
<div id="preview-modal" class="modal-overlay preview-overlay" style="display: none;" aria-hidden="true"></div>
<div id="video-modal" class="modal-overlay video-overlay" style="display: none;" aria-hidden="true"></div>
<div id="viewer-modal" class="modal-overlay file-viewer-overlay" style="display: none;" aria-hidden="true"></div>
<div id="new-drive-modal" class="modal-overlay" style="display: none;"></div>
<div id="join-drive-modal" class="modal-overlay" style="display: none;"></div>
<div id="share-drive-modal" class="modal-overlay" style="display: none;"></div>
<div id="join-requests-modal" class="modal-overlay" style="display: none;"></div>
<div id="upload-options-modal" class="modal-overlay" style="display: none;"></div>
<div id="import-options-modal" class="modal-overlay" style="display: none;"></div>
<div id="encryption-setup-modal" class="modal-overlay" style="display: none;"></div>
<div id="encryption-password-modal" class="modal-overlay" style="display: none;"></div>
<div id="encryption-settings-modal" class="modal-overlay" style="display: none;"></div>
<div id="mount-selection-modal" class="modal-overlay" style="display: none;" aria-hidden="true"></div>
<div id="logout-modal" class="modal-overlay" style="display: none;"></div>
<div id="leave-drive-modal" class="modal-overlay" style="display: none;"></div>
