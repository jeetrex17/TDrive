import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import MountSelectionModal from './MountSelectionModal.svelte';
import { mountSelection } from './mount-selection-store';

let host: HTMLElement | null = null;
let component: Record<string, unknown> | null = null;

async function settle(): Promise<void> {
    await Promise.resolve();
    await tick();
    flushSync();
}

function click(selector: string): void {
    const target = host?.querySelector<HTMLElement>(selector);
    if (!target) throw new Error(`missing ${selector}`);
    target.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    flushSync();
}

async function renderModal(): Promise<void> {
    host = document.createElement('div');
    host.id = 'mount-selection-modal';
    document.body.appendChild(host);
    component = mount(MountSelectionModal, { target: host });
    await settle();
}

afterEach(async () => {
    if (component) await unmount(component);
    host?.remove();
    component = null;
    host = null;
    mountSelection.reset();
});

describe('MountSelectionModal', () => {
    it('renders an accessible all-selected checklist with shared drives marked read only', async () => {
        mountSelection.open([
            { id: 20, title: 'Project', kind: 'shared' },
            { id: 10, title: 'Personal', kind: 'personal' },
        ], vi.fn());
        await renderModal();

        const dialog = host?.querySelector('[role="dialog"]');
        const choices = Array.from(host?.querySelectorAll<HTMLInputElement>('input[type="checkbox"]') ?? []);
        expect(dialog?.getAttribute('aria-labelledby')).toBe('mount-selection-title');
        expect(choices.map((choice) => choice.value)).toEqual(['10', '20']);
        expect(choices.every((choice) => choice.checked)).toBe(true);
        expect(host?.textContent).toContain('Project');
        expect(host?.textContent).toContain('Shared · Read only');
        expect(host?.querySelector<HTMLButtonElement>('#mount-selection-confirm')?.disabled).toBe(false);
    });

    it('confirms only checked drives and disables Mount when none are selected', async () => {
        const confirm = vi.fn();
        mountSelection.open([
            { id: 10, title: 'Personal', kind: 'personal' },
            { id: 20, title: 'Project', kind: 'shared' },
        ], confirm);
        await renderModal();

        click('input[value="20"]');
        click('#mount-selection-confirm');
        await settle();
        expect(confirm).toHaveBeenCalledWith([10]);

        mountSelection.open([{ id: 10, title: 'Personal', kind: 'personal' }], vi.fn());
        await settle();
        click('input[value="10"]');
        expect(host?.querySelector<HTMLButtonElement>('#mount-selection-confirm')?.disabled).toBe(true);
    });

    it('cancels without mounting', async () => {
        const confirm = vi.fn();
        mountSelection.open([{ id: 10, title: 'Personal', kind: 'personal' }], confirm);
        await renderModal();

        click('#mount-selection-cancel');
        await settle();

        expect(confirm).not.toHaveBeenCalled();
        expect(host?.style.display).toBe('none');
    });
});
