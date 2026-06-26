import { describe, expect, it } from 'vitest';
import {
    Button,
    ContextMenu,
    DriveList,
    FileList,
    FileState,
    FolderModal,
    IconButton,
    LeaveDriveModal,
    ModalShell,
    ProgressBar,
    SelectionBar,
    StateView,
    mountSvelte,
} from '.';

describe('Svelte UI primitives', () => {
    it('exports stable component entry points', () => {
        expect(Button).toBeTruthy();
        expect(IconButton).toBeTruthy();
        expect(ProgressBar).toBeTruthy();
        expect(StateView).toBeTruthy();
        expect(ContextMenu).toBeTruthy();
        expect(FileList).toBeTruthy();
        expect(FileState).toBeTruthy();
        expect(DriveList).toBeTruthy();
        expect(ModalShell).toBeTruthy();
        expect(FolderModal).toBeTruthy();
        expect(LeaveDriveModal).toBeTruthy();
        expect(SelectionBar).toBeTruthy();
    });

    it('exports a hybrid mount helper', () => {
        expect(typeof mountSvelte).toBe('function');
    });
});
