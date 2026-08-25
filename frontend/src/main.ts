// TDrive Frontend - Entry Point
// This file bootstraps the application by importing and initializing all modules

import { state } from './state';
import { setupAppShell } from './modules/app-shell';

// Import setup functions from modules
import { setupSelectionBar } from './modules/selection';
import { setupDownloadProgress, setupUploadProgress, setupUploadMenu, setupFileDrop, uploadWithParentID } from './modules/transfers';
import { setupBreadcrumb } from './modules/navigation';
import { setupContextMenu } from './modules/context-menu';
import { setupAuthWindowBindings, checkStatusAndShowScreen, hideAllScreens } from './modules/auth';
import { setupFileListWindowBindings, refreshFiles } from './modules/file-list';
import { setupGallery } from './modules/gallery';
import { setupSearchBar, runGlobalSearch } from './modules/search';
import { setupRefreshShortcut } from './modules/refresh-shortcut';

// Import modal setup functions
import { setupDeleteModal, openDeleteModal } from './modules/modals/delete';
import { setupRenameModal } from './modules/modals/rename';
import { setupMoveModal } from './modules/modals/move';
import { setupFolderModal, openNewFolderModal } from './modules/modals/folder';
import { setupPreviewModal } from './modules/modals/preview';
import { setupVideoModal } from './modules/modals/video';
import { setupFileViewerModal } from './modules/modals/file-viewer';
import { setupNewDriveModal } from './modules/modals/new-drive';
import { setupJoinDriveModal } from './modules/modals/join-drive';
import { setupShareDriveModal } from './modules/modals/share-drive';
import { setupLeaveDriveModal } from './modules/modals/leave-drive';
import { setupJoinRequestsModal } from './modules/modals/join-requests';
import { setupEncryptionSetupModal } from './modules/modals/encryption-setup';
import { setupEncryptionPasswordModal } from './modules/modals/encryption-password';
import { setupEncryptionSettingsModal } from './modules/modals/encryption-settings';
import { setupUploadOptionsModal } from './modules/modals/upload-options';
import { setupImportOptionsModal } from './modules/modals/import-options';
import { setupLogoutModal } from './modules/modals/logout';
import { setupMountSelectionModal } from './modules/modals/mount-selection';

// Sidebar / drives
import { setupSidebar, renderSidebar } from './modules/sidebar';
import { bindChannelsRenderers, refreshActiveDrive, setupLiveSyncEvents } from './modules/channels';

// Notifications
import { setupNotifications } from './modules/notifications';
import { setupNotifBell } from './modules/notif-bell';

// Profile menu (top-right avatar dropdown)
import { setupProfileMenu } from './modules/profile-menu';

// Setup window bindings that need to be available globally
window.refreshFiles = refreshFiles;
window.triggerRefresh = function() {
    if (String(state.searchQuery || "").trim()) {
        runGlobalSearch();
        return;
    }
    // Manual refresh: pull new ops from Telegram, then re-render. Awaitable
    // for callers that want to show progress, but most click handlers don't.
    return refreshActiveDrive();
};
window.openNewFolderModal = openNewFolderModal;
window.selectFile = function() {
    uploadWithParentID(state.currentFolderId);
};
window.initDeleteFolder = function(folderID, folderName) {
    openDeleteModal({ type: "folder", id: folderID, name: folderName || "" });
};
window.initDelete = function(id, name) {
    openDeleteModal({ type: "file", id, name: name || "" });
};

// The Wails runtime (`window.runtime`) and bound Go methods (`window.go`) are
// injected by the webview, not bundled by us. With the Vite dev server they can
// land a tick after `window.onload` fires, and until then any `window.runtime`
// access throws — which aborts the whole boot before any screen is shown and
// leaves a blank window. Wait for them first. A packaged build has them
// immediately (resolves at once); the timeout stops a genuinely missing runtime
// from hanging boot forever, letting the startup error toast surface instead.
function waitForWailsRuntime(timeoutMs = 4000): Promise<void> {
    const ready = () => Boolean(window.runtime?.EventsOn && window.go?.main?.App);
    if (ready()) return Promise.resolve();
    return new Promise((resolve) => {
        const start = Date.now();
        const tick = () => {
            if (ready() || Date.now() - start >= timeoutMs) resolve();
            else setTimeout(tick, 30);
        };
        tick();
    });
}

// Application initialization
window.onload = async function() {
    console.log("App loaded. Checking Status...");
    await waitForWailsRuntime();
    setupAppShell();
    hideAllScreens();

    // Notifications surface — must be set up before any other module that
    // might emit toasts. Bell is the unified history; toasts feed into it.
    setupNotifBell();
    setupNotifications();

    // Setup all modals
    setupDeleteModal();
    setupFolderModal();
    setupRenameModal();
    setupMoveModal();
    setupPreviewModal();
    setupVideoModal();
    setupFileViewerModal();
    setupNewDriveModal();
    setupJoinDriveModal();
    setupShareDriveModal();
    setupLeaveDriveModal();
    setupJoinRequestsModal();
    setupEncryptionSetupModal();
    setupEncryptionPasswordModal();
    setupEncryptionSettingsModal();
    setupUploadOptionsModal();
    setupImportOptionsModal();
    setupLogoutModal();
    setupMountSelectionModal();
    setupProfileMenu();

    // Setup UI components
    setupBreadcrumb();
    setupContextMenu();
    setupSelectionBar();
    setupDownloadProgress();
    setupUploadProgress();
    setupUploadMenu();
    setupFileDrop();
    setupSearchBar();
    setupRefreshShortcut();
    setupGallery();

    // Sidebar — wire renderers BEFORE setup so the first render finds the
    // right callbacks. setupSidebar will trigger an initial empty render;
    // auth.js loads channels after InitDrive succeeds.
    bindChannelsRenderers({
        onSidebarUpdate: () => renderSidebar(),
        onActiveDriveChanged: () => refreshFiles(),
    });
    setupSidebar();

    // Setup window bindings
    setupAuthWindowBindings();
    setupFileListWindowBindings();
    setupLiveSyncEvents();

    // Check status and show appropriate screen
    await checkStatusAndShowScreen();
};
