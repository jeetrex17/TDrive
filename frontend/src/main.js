// TDrive Frontend - Entry Point
// This file bootstraps the application by importing and initializing all modules

import { state } from './state.js';

// Import setup functions from modules
import { setupSelectionBar } from './modules/selection.js';
import { setupDownloadProgress, setupUploadProgress, uploadWithParentID } from './modules/transfers.js';
import { setupBreadcrumb } from './modules/navigation.js';
import { setupContextMenu } from './modules/context-menu.js';
import { setupPasswordReveal, setupAuthWindowBindings, checkStatusAndShowScreen } from './modules/auth.js';
import { setupFileListWindowBindings, refreshFiles } from './modules/file-list.js';
import { setupSearchBar, runGlobalSearch } from './modules/search.js';

// Import modal setup functions
import { setupDeleteModal, openDeleteModal } from './modules/modals/delete.js';
import { setupRenameModal } from './modules/modals/rename.js';
import { setupMoveModal } from './modules/modals/move.js';
import { setupFolderModal, openNewFolderModal } from './modules/modals/folder.js';
import { setupPreviewModal } from './modules/modals/preview.js';
import { setupNewDriveModal } from './modules/modals/new-drive.js';
import { setupJoinDriveModal } from './modules/modals/join-drive.js';
import { setupShareDriveModal } from './modules/modals/share-drive.js';
import { setupLeaveDriveModal } from './modules/modals/leave-drive.js';
import { setupJoinRequestsModal } from './modules/modals/join-requests.js';

// Sidebar / drives
import { setupSidebar, renderSidebar } from './modules/sidebar.js';
import { bindChannelsRenderers, refreshActiveDrive } from './modules/channels.js';

// Notifications
import { setupNotifications } from './modules/notifications.js';
import { setupNotifBell } from './modules/notif-bell.js';

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

// Application initialization
window.onload = async function() {
    console.log("App loaded. Checking Status...");

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
    setupNewDriveModal();
    setupJoinDriveModal();
    setupShareDriveModal();
    setupLeaveDriveModal();
    setupJoinRequestsModal();

    // Setup UI components
    setupBreadcrumb();
    setupContextMenu();
    setupSelectionBar();
    setupDownloadProgress();
    setupUploadProgress();
    setupPasswordReveal();
    setupSearchBar();

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

    // Check status and show appropriate screen
    await checkStatusAndShowScreen();
};
