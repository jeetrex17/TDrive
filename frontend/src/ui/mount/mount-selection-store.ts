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

function normalizeDrives(drives: readonly MountableDrive[]): MountableDrive[] {
    const seen = new Set<number>();
    return drives
        .filter((drive) => {
            if (!Number.isSafeInteger(drive.id) || drive.id <= 0 || seen.has(drive.id)) return false;
            seen.add(drive.id);
            return drive.kind === 'personal' || drive.kind === 'shared';
        })
        .map((drive) => ({ ...drive }))
        .sort((left, right) => {
            if (left.kind === right.kind) return 0;
            return left.kind === 'personal' ? -1 : 1;
        });
}

function createMountSelection() {
    const store = writable<MountSelectionState>({ ...EMPTY_STATE });
    let rememberedIds: number[] | null = null;
    let onConfirm: ConfirmSelection | null = null;

    function close(): void {
        onConfirm = null;
        store.set({ ...EMPTY_STATE });
    }

    function open(drives: readonly MountableDrive[], confirm: ConfirmSelection): void {
        const normalized = normalizeDrives(drives);
        const availableIds = new Set(normalized.map((drive) => drive.id));
        const remembered = rememberedIds?.filter((id) => availableIds.has(id)) ?? [];
        const selectedIds = remembered.length > 0
            ? remembered
            : normalized.map((drive) => drive.id);

        onConfirm = confirm;
        store.set({
            open: true,
            drives: normalized,
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
