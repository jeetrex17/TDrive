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

// Setup window bindings that need to be available globally
window.refreshFiles = refreshFiles;
window.triggerRefresh = function() {
    if (String(state.searchQuery || "").trim()) {
        runGlobalSearch();
        return;
    }
    refreshFiles();
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

    // Setup all modals
    setupDeleteModal();
    setupFolderModal();
    setupRenameModal();
    setupMoveModal();
    setupPreviewModal();

    // Setup UI components
    setupBreadcrumb();
    setupContextMenu();
    setupSelectionBar();
    setupDownloadProgress();
    setupUploadProgress();
    setupPasswordReveal();
    setupSearchBar();

    // Setup window bindings
    setupAuthWindowBindings();
    setupFileListWindowBindings();

    // Check status and show appropriate screen
    await checkStatusAndShowScreen();
};
