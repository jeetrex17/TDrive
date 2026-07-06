import { writable } from 'svelte/store';

// The delete modal renders pre-computed copy: the wording rules (bulk counts,
// owner-only skips, folder-subtree warnings) live in modules/modals/delete.ts
// next to the deletion logic, so the component stays a dumb confirm dialog.
export interface DeleteModalView {
    open: boolean;
    title: string;
    subtitle: string;
    confirmLabel: string;
}

const initialState: DeleteModalView = {
    open: false,
    title: '',
    subtitle: '',
    confirmLabel: 'Delete',
};

export const deleteModalState = writable<DeleteModalView>(initialState);

export function openDeleteModalView(view: Omit<DeleteModalView, 'open'>): void {
    deleteModalState.set({ open: true, ...view });
}

export function closeDeleteModalView(): void {
    deleteModalState.set(initialState);
}
