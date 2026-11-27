<script lang="ts">
    import { navDirection } from './file-list/nav-direction';
    import ArrowDownIcon from '@lucide/svelte/icons/arrow-down';
    import ArrowUpIcon from '@lucide/svelte/icons/arrow-up';
    import FolderPlusIcon from '@lucide/svelte/icons/folder-plus';
    import ImagesIcon from '@lucide/svelte/icons/images';
    import Link2Icon from '@lucide/svelte/icons/link-2';
    import SearchIcon from '@lucide/svelte/icons/search';
    import Trash2Icon from '@lucide/svelte/icons/trash-2';
    import { isMobilePlatform, listChannelSourceTopics, listChannelMedia, listMountableDrives } from '../api';
    import tdriveLogo from '../assets/images/tdrive-logo.png';
    import { openEncryptionSettingsModal } from '../modules/modals/encryption-settings';
    import { openLogoutModal } from '../modules/modals/logout';
    import { breadcrumbDrag, navigateBack, navigateHistory, navigateToIndex } from '../modules/navigation';
    import { forgetPendingFileFocus, goToDrivePath } from '../modules/path-navigation';
    import { folderHistoryState } from '../modules/folder-history';
    import { cancelTransfersInDirection, cancelUploadFile, clearHistory } from '../modules/notif-bell';
    import { ensureProfileLoaded } from '../modules/profile-menu';
    import { askEmptyTrash, openTrash, trashBusyKey, trashEntries } from '../modules/trash/controller';
    import {
        channelPhoto,
        loadChannelSources,
        openChannelPicker,
        openChannelPost,
        openInTelegram,
        recentChannelPages,
        showChannel,
        showChannelActions,
        showPostActions,
    } from '../modules/channel-sources';
    import { openSourceCopy } from '../modules/source-copies';
    import { clearSelection, trashSelectedItems, openSelectedItemsDownload, openSelectedItemsMove } from '../modules/selection';
    import { chooseFilesForCurrentFolder, chooseFolderForCurrentFolder } from '../modules/transfers';
    import { openNewFolderModal } from '../modules/modals/folder';
    import {
        handleDriveClick,
        handlePendingClick,
        showPendingActionsMenu,
        showSharedActionsMenu,
    } from '../modules/sidebar';
    import { setFileSortKey, fileSortState } from './file-list/file-sort-store';
    import { FILE_LIST_COLUMN_LABEL, fileListColumnMode } from './file-list/column-mode-store';
    import type { FileSortKey } from './file-list/file-sort';
    import Breadcrumb from './chrome/Breadcrumb.svelte';
    import ProfileMenu from './chrome/ProfileMenu.svelte';
    import SettingsWindow from './settings/SettingsWindow.svelte';
    import UploadMenu from './chrome/UploadMenu.svelte';
    import PhotosModeBar from './gallery/PhotosModeBar.svelte';
    import PhotosSurface from './gallery/PhotosSurface.svelte';
    import NotifBell from './notifications/NotifBell.svelte';
    import SelectionBar from './selection/SelectionBar.svelte';
    import DriveList from './sidebar/DriveList.svelte';
    import MountControl from './mount/MountControl.svelte';
    import FeatureLayer from './app/FeatureLayer.svelte';
    import ChannelNav from './channels/ChannelNav.svelte';
    import ChannelView from './channels/ChannelView.svelte';
    import { channelSources, openChannel } from './channels/channel-store';
    import { sourceKey } from './channels/channel-model';

    interface Props {
        dashboardVisible: boolean;
    }

    let { dashboardVisible }: Props = $props();
    let channelHeaderHost = $state<HTMLElement | null>(null);

    // OS mounts are desktop-only; the phone builds ship no WebDAV connector.
    const mountAvailable = !isMobilePlatform();
    // Desktop-only chrome: phones update through their app stores.
    const updaterAvailable = !isMobilePlatform();

    function sortButtonLabel(key: FileSortKey): string {
        const active = $fileSortState.key === key;
        if (!active) return `Sort by ${key}`;
        return `Sort by ${key} ${$fileSortState.direction === 'asc' ? 'descending' : 'ascending'}`;
    }

    function sortAriaValue(key: FileSortKey): 'ascending' | 'descending' | 'none' {
        if ($fileSortState.key !== key) return 'none';
        return $fileSortState.direction === 'asc' ? 'ascending' : 'descending';
    }

    // The list slides a little in the direction the reader moved, like the phone.
    const folderMove = navDirection();
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

<!-- The desktop dashboard. The typed root owns visibility; FeatureLayer owns
     the modal layer. Live chrome is gated on dashboardVisible so its listeners
     and the last session's rows live exactly as long as the dashboard does. -->
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
                <!-- The drive's own places lead the sidebar without a heading:
                     the drive row already says "My Drive". -->
                <div class="drives-section">
                    <div class="drives-list">
                        {#if dashboardVisible}
                            <DriveList kind="personal" onDriveClick={handleDriveClick} />
                        {/if}
                    </div>
                    <div class="drives-list">
                        <button id="nav-photos" class="drive-item nav-photos-item" type="button" title="Media">
                            <ImagesIcon class="icon" size={18} strokeWidth={2} aria-hidden="true" />
                            <span class="drive-item-title">Media</span>
                        </button>
                        <!-- Trash sits with the drive's other destinations,
                             where a user who just lost a file looks for it,
                             rather than behind the account menu. -->
                        <button
                            id="nav-trash"
                            class="drive-item"
                            type="button"
                            title="Trash"
                            onclick={openTrash}
                        >
                            <Trash2Icon class="icon" size={18} strokeWidth={2} aria-hidden="true" />
                            <span class="drive-item-title">Trash</span>
                        </button>
                    </div>
                </div>

                <div class="drives-section">
                    <div class="drives-section-title">Shared with me</div>
                    <div class="drives-list">
                        {#if dashboardVisible}
                            <DriveList
                                kind="shared"
                                onDriveClick={handleDriveClick}
                                onDriveActions={showSharedActionsMenu}
                                onPendingClick={handlePendingClick}
                                onPendingActions={showPendingActionsMenu}
                            />
                        {/if}
                    </div>
                </div>
                <!-- Telegram channels the user added: read-only places beside
                     the drives, opened in the main area without changing the
                     active drive. -->
                {#if dashboardVisible}
                    <div class="drives-section">
                        <ChannelNav
                            sources={$channelSources}
                            activeKey={$openChannel ? sourceKey($openChannel) : null}
                            loadPhoto={channelPhoto}
                            onSelect={showChannel}
                            onActions={showChannelActions}
                            onAdd={openChannelPicker}
                            onRetry={loadChannelSources}
                        />
                    </div>
                {/if}
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
            <p>Storage<span id="storage-used">—</span></p>
        </div>
    </aside>

    <main class="main-content" class:channel-mode={dashboardVisible && $openChannel !== null}>
        <!-- The folder path is the page title, so it leads the top bar
             instead of taking a band of its own under it. -->
        <header>
            <div class="drive-breadcrumb">
                <!-- The back button and the crumb trail are flex items of this row.
                     The host they used to arrive in was display:contents, so it was
                     never a box of its own and taking it away moves nothing. -->
                {#if dashboardVisible}
                    <Breadcrumb
                        onNavigate={navigateToIndex}
                        onBack={navigateBack}
                        onHistory={(step) => void navigateHistory(step)}
                        canGoBack={$folderHistoryState.canGoBack}
                        canGoForward={$folderHistoryState.canGoForward}
                        drag={breadcrumbDrag}
                        onPathSubmit={goToDrivePath}
                        onPathEditStart={forgetPendingFileFocus}
                    />
                {/if}
                <div id="gallery-title" class="gallery-title">Media</div>
                <PhotosModeBar />
                <div id="trash-title" class="trash-title">Trash</div>
                <!-- Destructive and irreversible, so it only ever opens the
                     confirmation. With nothing to empty it is absent rather than
                     disabled: a dead control invites a click that cannot do
                     anything. It is disabled only while a mutation is in flight. -->
                {#if $trashEntries.length > 0}
                    <button
                        id="trash-empty-btn"
                        class="trash-empty-btn"
                        type="button"
                        disabled={$trashBusyKey !== ''}
                        onclick={askEmptyTrash}
                    >
                        <Trash2Icon size={15} strokeWidth={2} aria-hidden="true" />
                        <span>Empty trash</span>
                    </button>
                {/if}
            </div>

            <!-- An open channel puts its title and view controls here, in
                 place of the folder path and the file search. -->
            <div class="channel-header-host" bind:this={channelHeaderHost}></div>

            <div class="search-bar">
                <SearchIcon class="search-icon" size={16} strokeWidth={2} aria-hidden="true" />
                <input id="search-input" type="text" placeholder="Search files..." autocomplete="off" spellcheck="false" aria-label="Search files">
            </div>

            <div class="header-actions">
                {#if dashboardVisible}
                    <NotifBell onCancelDirection={cancelTransfersInDirection} onCancelFile={cancelUploadFile} onClearHistory={clearHistory} />
                {/if}

                <!-- The menu's popover anchors to this relative wrapper. -->
                <div class="upload-menu-wrap">
                    {#if dashboardVisible}
                        <UploadMenu
                            onFiles={chooseFilesForCurrentFolder}
                            onFolder={chooseFolderForCurrentFolder}
                            onNewFolder={openNewFolderModal}
                        />
                    {/if}
                </div>
                <!-- The account menu is a plain child of the header: its popover
                     is positioned against .header-actions, which is the nearest
                     positioned ancestor either way, so nothing about where it
                     lands changes by rendering it here. Held back until the
                     dashboard is up because the menu opens itself when something
                     asks for the updates panel, and that request can come from
                     the login screen -- mounted earlier, the user would arrive at
                     the dashboard with the account menu already hanging open. -->
                {#if dashboardVisible}
                    <ProfileMenu
                        {updaterAvailable}
                        onOpen={ensureProfileLoaded}
                        onLogout={openLogoutModal}
                    />
                {/if}
            </div>
        </header>

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
                    <span>{FILE_LIST_COLUMN_LABEL[$fileListColumnMode]}</span>
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
                <span class="sr-only">Actions</span>
            </div>
        </div>

        <div id="file-list" class="file-list-box" data-file-drop-target data-nav={$folderMove ?? undefined} role="grid" aria-label="Files" aria-multiselectable="true" aria-colcount="4" tabindex="-1"></div>

        <!-- Floats over the list while rows are selected; the selection
             controller reveals it. -->
        <div id="selection-bar" class="selection-bar" style="display: none;" role="toolbar" aria-label="Selection">
            {#if dashboardVisible}
                <SelectionBar
                    onDownload={openSelectedItemsDownload}
                    onMove={openSelectedItemsMove}
                    onDelete={trashSelectedItems}
                    onClear={clearSelection}
                />
            {/if}
        </div>

        <!-- The gallery's scroll container: the thumbnail observer, click
             delegation and the viewer's return-to-grid find it by this id. -->
        <div id="gallery-view" class="gallery-view" tabindex="-1" aria-label="Media">
            {#if dashboardVisible}
                <PhotosSurface />
            {/if}
        </div>
        <!-- Keyed by connection so re-adding a channel starts a fresh view
             instead of paging with the old connection. -->
        {#if dashboardVisible && $openChannel}
            {#key sourceKey($openChannel)}
                <ChannelView
                    source={$openChannel}
                    fetchMedia={listChannelMedia}
                        fetchTopics={listChannelSourceTopics}
                    onOpenPost={openChannelPost}
                    onOpenTelegram={openInTelegram}
                    onPostActions={showPostActions}
                    onSavePosts={openSourceCopy}
                    recentPages={recentChannelPages}
                    headerHost={channelHeaderHost}
                    loadPhoto={channelPhoto}
                />
            {/key}
        {/if}
    </main>
</div>

<!-- Before the feature layer, so a dialog Settings hands off to (logging out,
     encryption) is drawn above it. -->
<div id="settings-modal" class="modal-overlay" style="display: none;" aria-hidden="true">
    {#if dashboardVisible}
        <SettingsWindow
            {updaterAvailable}
            onEncryptionSettings={openEncryptionSettingsModal}
            onLogout={openLogoutModal}
        />
    {/if}
</div>

<FeatureLayer />
