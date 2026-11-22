import { writable, type Readable } from 'svelte/store';
import { getMountDriveLetter, safeMountError, setMountDriveLetter, type MountDriveLetter } from '../../api';

export interface MountLetterApi {
    load(): Promise<MountDriveLetter>;
    save(letter: string): Promise<MountDriveLetter>;
}

export interface MountLetterState {
    /** False until the backend answers, and on platforms without drive letters. */
    available: boolean;
    letter: string;
    saving: boolean;
    error: string;
}

export interface MountLetterStore extends Readable<MountLetterState> {
    load(): Promise<void>;
    choose(letter: string): Promise<void>;
}

const INITIAL: MountLetterState = { available: false, letter: 'T:', saving: false, error: '' };

export function createMountLetterStore(api: MountLetterApi = {
    load: getMountDriveLetter,
    save: setMountDriveLetter,
}): MountLetterStore {
    const state = writable<MountLetterState>({ ...INITIAL });
    let loaded: Promise<void> | null = null;

    function load(): Promise<void> {
        loaded ??= api.load().then(
            (view) => state.update((current) => ({ ...current, ...view, error: '' })),
            // Leave the setting hidden; the next Settings open asks again.
            () => { loaded = null; },
        );
        return loaded;
    }

    async function choose(letter: string): Promise<void> {
        let previous = '';
        state.update((current) => {
            previous = current.letter;
            return { ...current, letter, saving: true, error: '' };
        });
        try {
            const view = await api.save(letter);
            state.update((current) => ({ ...current, ...view, saving: false }));
        } catch (error) {
            state.update((current) => ({
                ...current,
                letter: previous,
                saving: false,
                error: safeMountError(error, 'The drive letter could not be saved. Try again.'),
            }));
        }
    }

    return { subscribe: state.subscribe, load, choose };
}

export const mountLetter = createMountLetterStore();
