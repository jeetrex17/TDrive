import { writable, type Writable } from 'svelte/store';

// Shared view-state shape for ModalShell-based dialogs: open/close, a busy
// flag that in-flight submits use to lock the controls, an inline error line,
// and an arbitrary payload set at open time. Business logic stays in
// modules/modals/*; components render this state and call back via props.
export interface ModalViewState<T> {
    open: boolean;
    busy: boolean;
    error: string;
    payload: T | null;
}

export interface ModalController<T> {
    state: Writable<ModalViewState<T>>;
    open(payload: T): void;
    close(): void;
    setBusy(busy: boolean): void;
    setError(error: string): void;
    setPayload(payload: T): void;
}

export function createModalController<T>(): ModalController<T> {
    const initial: ModalViewState<T> = { open: false, busy: false, error: '', payload: null };
    const state = writable<ModalViewState<T>>(initial);
    return {
        state,
        open(payload: T): void {
            state.set({ open: true, busy: false, error: '', payload });
        },
        close(): void {
            state.set({ ...initial });
        },
        setBusy(busy: boolean): void {
            state.update((s) => ({ ...s, busy }));
        },
        setError(error: string): void {
            state.update((s) => ({ ...s, error }));
        },
        setPayload(payload: T): void {
            state.update((s) => ({ ...s, payload }));
        },
    };
}
