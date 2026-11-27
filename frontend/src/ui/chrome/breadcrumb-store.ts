import { writable } from 'svelte/store';

export interface BreadcrumbEntry {
    id: string;
    name: string;
}

// Drag-to-move plumbing stays in modules (navigation + drag-drop); the
// component only forwards DOM events with the crumb's folder id.
export interface BreadcrumbDrag {
    isActive: () => boolean;
    canDrop: (folderId: string) => boolean;
    highlight: (el: HTMLElement, allowed: boolean) => void;
    leave: (el: HTMLElement) => void;
    dropOn: (el: HTMLElement, folderId: string) => void;
    registerRoot: (el: HTMLElement) => void;
}

// Render bridge for the breadcrumb. state.folderPath stays the source of
// truth (several modules mutate it); modules/navigation.ts mirrors it here
// on every renderBreadcrumb() call.
export const breadcrumbPath = writable<BreadcrumbEntry[]>([]);

// Bumped to ask the breadcrumb to open its path field (Ctrl/Cmd+L); the
// component acts on each new value.
export const breadcrumbEditRequest = writable(0);

export function requestBreadcrumbEdit(): void {
    breadcrumbEditRequest.update((request) => request + 1);
}
