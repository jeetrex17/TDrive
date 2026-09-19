import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import SwitchRow from './SwitchRow.svelte';

let component: Record<string, unknown> | null = null;
let host: HTMLElement | null = null;

function setup(props: { title: string; description?: string; checked: boolean; disabled?: boolean; onchange: (checked: boolean) => void }): HTMLButtonElement {
    host = document.createElement('div');
    document.body.appendChild(host);
    component = mount(SwitchRow, { target: host, props });
    flushSync();
    const control = host.querySelector<HTMLButtonElement>('[role="switch"]');
    if (!control) throw new Error('switch did not render');
    return control;
}

afterEach(async () => {
    if (component) await unmount(component);
    host?.remove();
    component = null;
    host = null;
});

describe('SwitchRow', () => {
    it('is one switch named by its title, with the description as its explanation', () => {
        const control = setup({ title: 'Videos', description: 'Includes screen recordings.', checked: true, onchange: () => {} });
        expect(control.getAttribute('aria-checked')).toBe('true');
        const titleId = control.getAttribute('aria-labelledby');
        const descriptionId = control.getAttribute('aria-describedby');
        expect(document.getElementById(titleId!)?.textContent).toBe('Videos');
        expect(document.getElementById(descriptionId!)?.textContent).toBe('Includes screen recordings.');
    });

    it('reports the flipped value, and nothing while disabled', () => {
        const onchange = vi.fn();
        const control = setup({ title: 'Photos', checked: false, onchange });
        control.click();
        expect(onchange).toHaveBeenCalledWith(true);

        const disabled = setup({ title: 'Wi-Fi only', checked: false, disabled: true, onchange });
        disabled.click();
        expect(onchange).toHaveBeenCalledTimes(1);
        expect(disabled.getAttribute('aria-describedby')).toBeNull();
    });

    it('gives two rows on one page distinct label ids', () => {
        const first = setup({ title: 'A', checked: false, onchange: () => {} });
        const second = setup({ title: 'B', checked: false, onchange: () => {} });
        expect(first.getAttribute('aria-labelledby')).not.toBe(second.getAttribute('aria-labelledby'));
    });
});
