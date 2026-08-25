import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import { flushSync } from 'svelte';
import { setupMountSelectionModal } from './mount-selection';
import { mountSelection } from '../../ui/mount/mount-selection-store';

let host: HTMLElement;

beforeAll(() => {
    host = document.createElement('div');
    host.id = 'mount-selection-modal';
    document.body.appendChild(host);
});

afterEach(() => {
    mountSelection.reset();
});

describe('setupMountSelectionModal', () => {
    it('mounts one modal instance when setup runs more than once', () => {
        setupMountSelectionModal();
        setupMountSelectionModal();
        mountSelection.open([
            { id: 10, title: 'Personal', kind: 'personal' },
            { id: 20, title: 'Project', kind: 'shared' },
        ], vi.fn());
        flushSync();

        expect(document.querySelectorAll('#mount-selection-modal')).toHaveLength(1);
        expect(host.querySelectorAll('[role="dialog"]')).toHaveLength(1);
    });
});
