<script lang="ts">
    import { isMobilePlatform } from '../../api';
    import { activateLiveSyncEvents } from '../../modules/channels';
    import { activateContextMenu } from '../../modules/context-menu';
    import { activateDropOverlay } from '../../modules/drop-overlay';
    import { renderEncryptionSettingsEntry } from '../../modules/encryption';
    import { activateFileList } from '../../modules/file-list';
    import { activateGallery } from '../../modules/gallery';
    import { renderBreadcrumb } from '../../modules/navigation';
    import {
        activateNotificationEffects,
        dismissNotification,
        pauseAllNotifications,
        pauseToast,
        resumeAllNotifications,
        resumeToast,
    } from '../../modules/notifications';
    import { ensureProfileLoaded } from '../../modules/profile-menu';
    import { activateConnectivityWatch } from '../../modules/connectivity';
    import { activateRefreshShortcut } from '../../modules/refresh-shortcut';
    import { activateSearchBar } from '../../modules/search';
    import {
        activateSelectionBar,
        clearSelection,
        openSelectedItemsDelete,
        openSelectedItemsMove,
    } from '../../modules/selection';
    import { activateSidebar } from '../../modules/sidebar';
    import {
        activateTransferSurfaces,
        chooseFilesForCurrentFolder,
        chooseFolderForCurrentFolder,
    } from '../../modules/transfers';
    import { activateUpdates } from '../../modules/updates';
    import { confirmDelete } from '../../modules/modals/delete';
    import {
        cancelEncryptionPassword,
        submitEncryptionPassword,
    } from '../../modules/modals/encryption-password';
    import {
        cancelEncryptionSettings,
        submitEncryptionSettings,
    } from '../../modules/modals/encryption-settings';
    import {
        cancelEncryptionSetup,
        submitEncryptionSetup,
    } from '../../modules/modals/encryption-setup';
    import { openNewFolderModal, submitFolder } from '../../modules/modals/folder';
    import {
        cancelImportOptions,
        confirmImportOptions,
        replanImportOptions,
    } from '../../modules/modals/import-options';
    import { submitJoinDrive } from '../../modules/modals/join-drive';
    import { resolveRequest } from '../../modules/modals/join-requests';
    import { confirmLeaveDrive } from '../../modules/modals/leave-drive';
    import { confirmLogout } from '../../modules/modals/logout';
    import {
        confirmMove,
        navigateMoveBack,
        navigateMoveCrumb,
        openMoveFolder,
    } from '../../modules/modals/move';
    import { submitNewDrive } from '../../modules/modals/new-drive';
    import { submitRename } from '../../modules/modals/rename';
    import {
        cancelUploadOptions,
        confirmUploadOptions,
    } from '../../modules/modals/upload-options';
    import UploadMenu from '../chrome/UploadMenu.svelte';
    import FileList from '../file-list/FileList.svelte';
    import Gallery from '../gallery/Gallery.svelte';
    import ContextMenu from '../menus/ContextMenu.svelte';
    import DeleteModal from '../modals/DeleteModal.svelte';
    import EncryptionPasswordModal from '../modals/EncryptionPasswordModal.svelte';
    import EncryptionSettingsModal from '../modals/EncryptionSettingsModal.svelte';
    import EncryptionSetupModal from '../modals/EncryptionSetupModal.svelte';
    import FolderModal from '../modals/FolderModal.svelte';
    import ImportOptionsModal from '../modals/ImportOptionsModal.svelte';
    import JoinDriveModal from '../modals/JoinDriveModal.svelte';
    import JoinRequestsModal from '../modals/JoinRequestsModal.svelte';
    import LeaveDriveModal from '../modals/LeaveDriveModal.svelte';
    import LogoutModal from '../modals/LogoutModal.svelte';
    import MoveModal from '../modals/MoveModal.svelte';
    import NewDriveModal from '../modals/NewDriveModal.svelte';
    import RenameModal from '../modals/RenameModal.svelte';
    import ShareDriveModal from '../modals/ShareDriveModal.svelte';
    import UploadOptionsModal from '../modals/UploadOptionsModal.svelte';
    import MountSelectionModal from '../mount/MountSelectionModal.svelte';
    import ToastStack from '../notifications/ToastStack.svelte';
    import PreviewModal from '../preview/PreviewModal.svelte';
    import SelectionBar from '../selection/SelectionBar.svelte';
    import DropOverlay from '../transfers/DropOverlay.svelte';
    import UpdatesPanel from '../updates/UpdatesPanel.svelte';
    import VideoModal from '../video/VideoModal.svelte';
    import FileViewerModal from '../viewers/FileViewerModal.svelte';
    import { appView } from './app-store';
    import FeaturePortal from './FeaturePortal.svelte';

    // Desktop-only chrome: phones update through their app stores.
    const updaterAvailable = !isMobilePlatform();
    // Android can pick a folder too: the app's own bridge walks the chosen
    // tree and the upload copies out of it a window at a time, so the item is
    // no longer a button that could only fail. See modules/android-folder.ts.
    const chooseFolder = chooseFolderForCurrentFolder;
    // The phone's FAB menu adds New folder; desktop creates folders from the
    // context menu, so it stays out of the upload button there.
    const newFolder = isMobilePlatform() ? openNewFolderModal : undefined;

    $effect(() => activateNotificationEffects());

    $effect(() => {
        if ($appView.kind !== 'dashboard') return;

        const disposers = [
            activateContextMenu(),
            activateSidebar(),
            activateSelectionBar(),
            activateFileList(),
            activateGallery(),
            activateDropOverlay(),
            activateTransferSurfaces(),
            activateSearchBar(),
            activateRefreshShortcut(),
            activateConnectivityWatch(),
            activateLiveSyncEvents(),
            activateUpdates(),
        ];
        let previewActivationCancelled = false;
        let deactivatePreview = () => {};

        void import('../../modules/modals/preview').then(({ activatePreviewModal }) => {
            if (previewActivationCancelled) return;
            deactivatePreview = activatePreviewModal();
        });

        renderBreadcrumb();
        renderEncryptionSettingsEntry();
        void ensureProfileLoaded();

        return () => {
            previewActivationCancelled = true;
            deactivatePreview();
            for (let index = disposers.length - 1; index >= 0; index -= 1) {
                disposers[index]();
            }
        };
    });
</script>

<div id="toast-stack" class="toast-stack" role="status" aria-live="polite">
    <ToastStack
        onDismiss={dismissNotification}
        onPauseToast={pauseToast}
        onResumeToast={resumeToast}
        onPauseAll={pauseAllNotifications}
        onResumeAll={resumeAllNotifications}
    />
</div>

{#if $appView.kind === 'dashboard'}
    <FeaturePortal hostId="upload-menu-root">
        <UploadMenu onFiles={chooseFilesForCurrentFolder} onFolder={chooseFolder} onNewFolder={newFolder} />
    </FeaturePortal>
    <FeaturePortal hostId="selection-bar">
        <SelectionBar
            onMove={openSelectedItemsMove}
            onDelete={openSelectedItemsDelete}
            onClear={clearSelection}
        />
    </FeaturePortal>
    <FeaturePortal hostId="file-list">
        <FileList />
    </FeaturePortal>
    <FeaturePortal hostId="gallery-view">
        <Gallery />
    </FeaturePortal>

    <div id="context-menu" class="context-menu">
        <ContextMenu />
    </div>
    <DropOverlay />
    <!-- The in-app updater is off on phones (store updates), and this top-level
         panel is an always-in-flow node that would bleed onto the phone shell. -->
    {#if updaterAvailable}
        <UpdatesPanel />
    {/if}
    <div id="mount-selection-modal" class="modal-overlay" style="display: none;" aria-hidden="true">
        <MountSelectionModal />
    </div>

    <div id="delete-modal" class="modal-overlay" style="display: none;" aria-hidden="true">
        <DeleteModal onConfirm={confirmDelete} />
    </div>
    <div id="rename-modal" class="modal-overlay" style="display: none;" aria-hidden="true">
        <RenameModal onSubmit={submitRename} />
    </div>
    <div id="folder-modal" class="modal-overlay" style="display: none;" aria-hidden="true">
        <FolderModal onSubmit={submitFolder} />
    </div>
    <div id="move-modal" class="modal-overlay" style="display: none;" aria-hidden="true">
        <MoveModal
            onOpenFolder={openMoveFolder}
            onCrumb={navigateMoveCrumb}
            onBack={navigateMoveBack}
            onConfirm={confirmMove}
        />
    </div>
    <div id="new-drive-modal" class="modal-overlay" style="display: none;" aria-hidden="true">
        <NewDriveModal onSubmit={submitNewDrive} />
    </div>
    <div id="join-drive-modal" class="modal-overlay" style="display: none;" aria-hidden="true">
        <JoinDriveModal onSubmit={submitJoinDrive} />
    </div>
    <div id="share-drive-modal" class="modal-overlay" style="display: none;" aria-hidden="true">
        <ShareDriveModal />
    </div>
    <div id="leave-drive-modal" class="modal-overlay" style="display: none;" aria-hidden="true">
        <LeaveDriveModal onConfirm={confirmLeaveDrive} />
    </div>
    <div id="join-requests-modal" class="modal-overlay" style="display: none;" aria-hidden="true">
        <JoinRequestsModal onAction={resolveRequest} />
    </div>
    <div id="encryption-setup-modal" class="modal-overlay" style="display: none;" aria-hidden="true">
        <EncryptionSetupModal onCancel={cancelEncryptionSetup} onSubmit={submitEncryptionSetup} />
    </div>
    <div id="encryption-password-modal" class="modal-overlay" style="display: none;" aria-hidden="true">
        <EncryptionPasswordModal onCancel={cancelEncryptionPassword} onSubmit={submitEncryptionPassword} />
    </div>
    <div id="encryption-settings-modal" class="modal-overlay" style="display: none;" aria-hidden="true">
        <EncryptionSettingsModal onCancel={cancelEncryptionSettings} onSubmit={submitEncryptionSettings} />
    </div>
    <div id="upload-options-modal" class="modal-overlay" style="display: none;" aria-hidden="true">
        <UploadOptionsModal onCancel={cancelUploadOptions} onConfirm={confirmUploadOptions} />
    </div>
    <div id="import-options-modal" class="modal-overlay" style="display: none;" aria-hidden="true">
        <ImportOptionsModal
            onCancel={cancelImportOptions}
            onConfirm={confirmImportOptions}
            onToggle={replanImportOptions}
        />
    </div>
    <div id="logout-modal" class="modal-overlay" style="display: none;" aria-hidden="true">
        <LogoutModal onConfirm={confirmLogout} />
    </div>

    <!-- Viewer controllers stay lazy because they load media/runtime dependencies only when used. -->
    <div id="viewer-modal" class="modal-overlay" style="display: none;" aria-hidden="true">
        <FileViewerModal
            onClose={() => {
                void import('../../modules/modals/file-viewer').then(({ closeFileViewer }) => closeFileViewer());
            }}
            onDownload={() => {
                void import('../../modules/modals/file-viewer').then(({ downloadActiveFile }) => downloadActiveFile());
            }}
        />
    </div>
    <div id="preview-modal" class="modal-overlay preview-overlay" style="display: none;" aria-hidden="true">
        <PreviewModal />
    </div>
    <div id="video-modal" class="modal-overlay video-overlay" style="display: none;" aria-hidden="true">
        <VideoModal
            onPreferencesChange={(value) => {
                void import('../../modules/modals/video').then(({ updatePlaybackPreferences }) =>
                    updatePlaybackPreferences(value),
                );
            }}
            onHidePlaylist={(restoreFocus) => {
                void import('../../modules/modals/video').then(({ hideVideoPlaylist }) =>
                    hideVideoPlaylist(restoreFocus),
                );
            }}
            onSelectPlaylistItem={(index) => {
                void import('../../modules/modals/video').then(({ selectVideoPlaylistItem }) =>
                    selectVideoPlaylistItem(index),
                );
            }}
            onUpdateVideoAutoNext={(enabled) => {
                void import('../../modules/modals/video').then(({ updateVideoAutoNext }) =>
                    updateVideoAutoNext(enabled),
                );
            }}
        />
    </div>
{/if}
