import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import { fromStore, writable, type Writable } from 'svelte/store';
import ChannelPickerModal from './ChannelPickerModal.svelte';
import type { ChannelSource } from './channel-model';

const source = (peerId: number, title: string, connected = false): ChannelSource => ({
    peerKind: 'channel', peerId, title, username: title.toLowerCase().replace(/ /g, ''), connected, protected: false, available: true, accountId: '7', generation: connected ? 'g' : '',
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

interface Parent {
    onAdd?: (source: ChannelSource) => Promise<void>;
    /** Props the parent can change after mounting. */
    open?: Writable<boolean>;
    added?: Writable<readonly ChannelSource[]>;
}

function render({ onAdd = vi.fn(async () => {}), open = writable(true), added = writable([JOINED[0]]) }: Parent = {}) {
    const isOpen = fromStore(open);
    const inSidebar = fromStore(added);
    const props = {
        get open() { return isOpen.current; },
        get added() { return inSidebar.current; },
        loadCandidates: vi.fn(async (): Promise<ChannelSource[]> => JOINED),
        resolvePublic: vi.fn(async (): Promise<ChannelSource> => JOINED[0]),
        loadPhoto: vi.fn(async () => ''),
        onAdd,
        onOpen: vi.fn(),
        onClose: vi.fn(),
    };
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
            'Connect Tech Talks', 'Connect Nature Docs', 'Open Field Recordings, already connected',
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
        const props = render({ onAdd: vi.fn(async () => { throw new Error('rpc error code 420: FLOOD_WAIT_12'); }) });
        await settle();
        rows()[0].click();
        await settle();
        expect(props.onClose).not.toHaveBeenCalled();
        expect(host?.querySelector('[role="alert"]')?.textContent).toContain('Tech Talks could not be added.');
        expect(rows().every((row) => !row.disabled)).toBe(true);
    });

    it('checks a public channel link before connecting and states that it will not join Telegram', async () => {
        const props = render();
        await settle();
        const input = host!.querySelector<HTMLInputElement>('#channel-public-link')!;
        input.value = 'https://t.me/fieldrecordings';
        input.dispatchEvent(new Event('input', { bubbles: true }));
        input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
        await settle();

        expect(props.resolvePublic).toHaveBeenCalledWith('https://t.me/fieldrecordings');
        expect(host?.textContent).toContain('never joins channels, sends messages, or starts bots');
        expect(host?.querySelector('.channel-picker-public-result')?.textContent).toContain('Field Recordings');
    });

    it('opens again on the last list while it refreshes, added as the sidebar has it now', async () => {
        const open = writable(true);
        const added = writable<readonly ChannelSource[]>([JOINED[0]]);
        const props = render({ open, added });
        await settle();
        open.set(false);
        await settle();

        // Removed from the sidebar meanwhile, and Telegram has yet to answer.
        added.set([]);
        props.loadCandidates.mockReturnValue(new Promise(() => {}));
        open.set(true);
        await settle();

        expect(props.loadCandidates).toHaveBeenCalledTimes(2);
        expect(host?.querySelector('.is-placeholder')).toBeNull();
        expect(rows().map((row) => row.getAttribute('aria-label'))).toEqual([
            'Connect Field Recordings', 'Connect Tech Talks', 'Connect Nature Docs',
        ]);
    });
});
