import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import ShareDriveModal from './ShareDriveModal.svelte';
import { shareDriveModal } from './share-drive-modal-store';

Object.defineProperty(HTMLElement.prototype, 'offsetParent', {
    configurable: true,
    get() {
        return this.parentElement;
    },
});

let host: HTMLElement;
let app: Record<string, unknown>;

beforeEach(() => {
    host = document.createElement('div');
    host.id = 'share-drive-modal';
    document.body.append(host);
    app = mount(ShareDriveModal, { target: host });
});

afterEach(async () => {
    shareDriveModal.close();
    flushSync();
    await unmount(app);
    host.remove();
});

describe('Invite link modal', () => {
    it('shows a selectable value without exposing an editable keyboard target', async () => {
        shareDriveModal.open({ link: 'https://t.me/+invite', approvalRequired: true });
        flushSync();
        await tick();
        flushSync();

        const link = host.querySelector<HTMLElement>('#share-drive-link');
        expect(link?.textContent).toBe('https://t.me/+invite');
        expect(link?.tagName).not.toBe('INPUT');
        expect(link?.getAttribute('contenteditable')).not.toBe('true');
        expect((document.activeElement as HTMLElement | null)?.id).toBe('share-drive-copy');
    });

    it('does not claim a failed clipboard fallback succeeded', async () => {
        Object.defineProperty(navigator, 'clipboard', {
            configurable: true,
            value: { writeText: vi.fn().mockRejectedValue(new Error('unavailable')) },
        });
        Object.defineProperty(document, 'execCommand', {
            configurable: true,
            value: vi.fn().mockReturnValue(false),
        });
        shareDriveModal.open({ link: 'https://t.me/+invite', approvalRequired: false });
        flushSync();
        await tick();
        flushSync();

        host.querySelector<HTMLButtonElement>('#share-drive-copy')?.click();
        await tick();
        flushSync();

        expect(host.querySelector('#share-drive-copy')?.textContent?.trim()).toBe('Copy failed');
    });
});
