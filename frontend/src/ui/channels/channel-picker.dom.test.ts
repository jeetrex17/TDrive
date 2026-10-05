import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import ChannelPickerModal from './ChannelPickerModal.svelte';
import type { ChannelSource } from './channel-model';

const source = (channelId: number, title: string, connected = false): ChannelSource => ({
    channelId, title, username: title.toLowerCase().replace(/ /g, ''), connected, protected: false, accountId: '7', generation: connected ? 'g' : '',
});
const JOINED = [source(50, 'Field Recordings', true), source(60, 'Tech Talks'), source(61, 'Nature Docs')];

let app: Record<string, unknown> | null = null;
let host: HTMLElement | null = null;

async function settle(): Promise<void> {
    for (let index = 0; index < 6; index += 1) {
        flushSync();
        await tick();
        await Promise.resolve();
    }
    flushSync();
}

function render(onAdd = vi.fn(async () => {})) {
    const props = { open: true, loadCandidates: vi.fn(async () => JOINED), loadPhoto: vi.fn(async () => ''), onAdd, onOpen: vi.fn(), onClose: vi.fn() };
    host = document.createElement('div');
    host.id = 'channel-picker-modal';
    document.body.append(host);
    app = mount(ChannelPickerModal, { target: host, props });
    return props;
}

function rows(): HTMLButtonElement[] {
    return Array.from(host?.querySelectorAll<HTMLButtonElement>('.channel-picker-row:not(.is-placeholder)') ?? []);
}

function search(value: string): HTMLInputElement {
    const input = host!.querySelector<HTMLInputElement>('#channel-picker-search')!;
    input.value = value;
    input.dispatchEvent(new Event('input', { bubbles: true }));
    return input;
}

afterEach(async () => {
    if (app) await unmount(app);
    host?.remove();
    app = null;
    host = null;
});

describe('ChannelPickerModal', () => {
    it('lists channels still to add first and opens one already added instead of adding it again', async () => {
        const props = render();
        await settle();
        expect(rows().map((row) => row.getAttribute('aria-label'))).toEqual([
            'Add Tech Talks', 'Add Nature Docs', 'Open Field Recordings, already added',
        ]);
        rows()[2].click();
        await settle();
        expect(props.onOpen).toHaveBeenCalledWith(JOINED[0]);
        expect(props.onAdd).not.toHaveBeenCalled();
        expect(props.onClose).toHaveBeenCalled();
    });

    it('adds the only match when Enter is pressed in the search field', async () => {
        const props = render();
        await settle();
        const input = search('NATURE');
        await settle();
        expect(rows()).toHaveLength(1);
        input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
        await settle();
        expect(props.onAdd).toHaveBeenCalledWith(JOINED[2]);
        expect(props.onClose).toHaveBeenCalled();
    });

    it('stays open and says why when adding fails', async () => {
        const props = render(vi.fn(async () => { throw new Error('rpc error code 420: FLOOD_WAIT_12'); }));
        await settle();
        rows()[0].click();
        await settle();
        expect(props.onClose).not.toHaveBeenCalled();
        expect(host?.querySelector('[role="alert"]')?.textContent).toContain('Tech Talks could not be added.');
        expect(rows().every((row) => !row.disabled)).toBe(true);
    });
});
