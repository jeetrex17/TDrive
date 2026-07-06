// Breadcrumb and folder navigation for TDrive frontend

import { state } from '../state';
import { canDropOnFolder, setDropHighlight, performDropMove } from './drag-drop';
import Breadcrumb from '../ui/chrome/Breadcrumb.svelte';
import { breadcrumbPath, type BreadcrumbDrag } from '../ui/chrome/breadcrumb-store';
import { mountSvelte, type SvelteMountHandle } from '../ui/mount';

let breadcrumbHandle: SvelteMountHandle<Record<string, unknown>> | null = null;

// renderBreadcrumb mirrors state.folderPath (the source of truth, mutated by
// several modules) into the breadcrumb store.
export function renderBreadcrumb() {
    breadcrumbPath.set(state.folderPath.map((f) => ({ id: f.id, name: f.name })));
}

const drag: BreadcrumbDrag = {
    isActive: () => Boolean(state.dragState),
    canDrop: (folderId) => canDropOnFolder(folderId),
    highlight: (el, allowed) => setDropHighlight(el, allowed),
    leave: (el) => {
        if (state.dragOverEl === el) {
            el.classList.remove('drop-target');
            el.classList.remove('drop-denied');
            state.dragOverEl = null;
        }
    },
    dropOn: (el, folderId) => {
        if (state.dragOverEl === el) state.dragOverEl = null;
        el.classList.remove('drop-target');
        el.classList.remove('drop-denied');
        void performDropMove(folderId);
    },
    registerRoot: (el) => {
        state.dragRootEl = el;
    },
};

function navigateToIndex(index: number) {
    if (index < 0) {
        state.folderPath = [];
        state.currentFolderId = '';
    } else {
        state.folderPath = state.folderPath.slice(0, index + 1);
        state.currentFolderId = state.folderPath[index]?.id || '';
    }
    // Breadcrumb navigation always exits any virtual view (e.g. Photos).
    state.virtualView = null;
    renderBreadcrumb();
    window.refreshFiles();
}

function navigateBack() {
    if (state.folderPath.length === 0) return;
    state.folderPath = state.folderPath.slice(0, -1);
    state.currentFolderId = state.folderPath.length ? state.folderPath[state.folderPath.length - 1].id : '';
    state.virtualView = null;
    renderBreadcrumb();
    window.refreshFiles();
}

export function setupBreadcrumb() {
    const host = document.getElementById('breadcrumb-root');
    if (!host || breadcrumbHandle) return;

    host.replaceChildren();
    breadcrumbHandle = mountSvelte(Breadcrumb, {
        target: host,
        props: {
            onNavigate: navigateToIndex,
            onBack: navigateBack,
            drag,
        },
    });
    renderBreadcrumb();
}

export function navigateToFolder(folderID: string, folderName: string) {
    state.folderPath = [...state.folderPath, { id: folderID, name: folderName }];
    state.currentFolderId = folderID;
    state.virtualView = null;
    renderBreadcrumb();
    window.refreshFiles();
}

export function ensureNotInsideDeletedFolder(deletedFolderID: string) {
    if (!deletedFolderID) return;

    const idx = state.folderPath.findIndex((f) => f.id === deletedFolderID);
    if (idx === -1) return;

    state.folderPath = state.folderPath.slice(0, idx);
    state.currentFolderId = state.folderPath.length ? state.folderPath[state.folderPath.length - 1].id : '';
    renderBreadcrumb();
}
