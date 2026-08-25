import { get, writable, type Readable } from 'svelte/store';
import type { MountableDrive } from '../../types';

export interface MountSelectionState {
    open: boolean;
    drives: MountableDrive[];
    selectedIds: number[];
}

type ConfirmSelection = (channelIds: number[]) => void;

const EMPTY_STATE: MountSelectionState = {
    open: false,
    drives: [],
    selectedIds: [],
};

function createMountSelection() {
    const store = writable<MountSelectionState>({ ...EMPTY_STATE });
    let rememberedIds: number[] | null = null;
    let onConfirm: ConfirmSelection | null = null;

    function close(): void {
        onConfirm = null;
        store.set({ ...EMPTY_STATE });
    }

    function open(drives: readonly MountableDrive[], confirm: ConfirmSelection): void {
        const availableDrives = drives.map((drive) => ({ ...drive }));
        const availableIds = new Set(availableDrives.map((drive) => drive.id));
        const remembered = rememberedIds?.filter((id) => availableIds.has(id)) ?? [];
        const selectedIds = remembered.length > 0
            ? remembered
            : availableDrives.map((drive) => drive.id);

        onConfirm = confirm;
        store.set({
            open: true,
            drives: availableDrives,
            selectedIds: [...selectedIds],
        });
    }

    function toggle(channelId: number): void {
        store.update((current) => {
            if (!current.open || !current.drives.some((drive) => drive.id === channelId)) return current;
            const selected = new Set(current.selectedIds);
            if (selected.has(channelId)) selected.delete(channelId);
            else selected.add(channelId);
            return {
                ...current,
                selectedIds: current.drives
                    .map((drive) => drive.id)
                    .filter((id) => selected.has(id)),
            };
        });
    }

    function confirm(): void {
        const selectedIds = [...get(store).selectedIds];
        if (selectedIds.length === 0 || !onConfirm) return;

        const confirmSelection = onConfirm;
        rememberedIds = [...selectedIds];
        close();
        confirmSelection([...selectedIds]);
    }

    function reset(): void {
        rememberedIds = null;
        close();
    }

    return {
        state: { subscribe: store.subscribe } satisfies Readable<MountSelectionState>,
        open,
        close,
        toggle,
        confirm,
        reset,
    };
}

export const mountSelection = createMountSelection();
