<script lang="ts">
    import { setFileSortKey, fileSortState } from './file-list/file-sort-store';
    import type { FileSortKey } from './file-list/file-sort';

    function refreshFiles(): void {
        void window.triggerRefresh?.();
    }

    function openFolderModal(): void {
        window.openNewFolderModal?.();
    }

    function sortButtonLabel(key: FileSortKey): string {
        const active = $fileSortState.key === key;
        if (!active) return `Sort by ${key}`;
        return `Sort by ${key} ${$fileSortState.direction === 'asc' ? 'descending' : 'ascending'}`;
    }

    function sortIndicator(key: FileSortKey): string {
        if ($fileSortState.key !== key) return '';
        return $fileSortState.direction === 'asc' ? '↑' : '↓';
    }
</script>

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
                            <svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="8.5" cy="8.5" r="1.6"/><path stroke-linecap="round" stroke-linejoin="round" d="M21 15l-5-5L5 21"/></svg>
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
                    <svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M12 4v16m8-8H4"/></svg>
                    New shared drive
                </button>
                <button id="open-join-drive" class="drive-action-btn" type="button" title="Join a shared drive via invite link">
                    <svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M10 14l11-11m0 0v6m0-6h-6"/><path stroke-linecap="round" stroke-linejoin="round" d="M21 13v6a2 2 0 01-2 2H5a2 2 0 01-2-2V5a2 2 0 012-2h6"/></svg>
                    Join with link
                </button>
            </div>
        </nav>

        <div class="storage-info">
            <p>Storage:<span id="storage-used">0 B / Unlimited</span></p>
        </div>
    </aside>

    <main class="main-content">
        <header>
            <div class="search-bar">
                <svg class="search-icon" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z"></path></svg>
                <input id="search-input" type="text" placeholder="Search files..." autocomplete="off" spellcheck="false" aria-label="Search files">
            </div>

            <div class="header-actions">
                <div id="notif-bell-root" style="display: contents;"></div>

                <button class="icon-btn" type="button" onclick={refreshFiles} title="Refresh">
                    <svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"></path></svg>
                </button>

                <button id="new-folder-btn" class="secondary-btn folder-btn" type="button" onclick={openFolderModal} title="New Folder">
                    <svg class="btn-icon" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 7a2 2 0 012-2h5l2 2h7a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2V7z"/><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 11v6m3-3H9"/></svg>
                    Folder
                </button>

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
                <span class="file-sort-indicator" aria-hidden="true">{sortIndicator('name')}</span>
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
                <span class="file-sort-indicator" aria-hidden="true">{sortIndicator('date')}</span>
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
                <span class="file-sort-indicator" aria-hidden="true">{sortIndicator('size')}</span>
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
