import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync } from 'svelte';
import { get } from 'svelte/store';
import { fileViewerState } from '../../ui/viewers/file-viewer-store';
import { canOpenFileViewer, closeFileViewer, openFileViewer, setupFileViewerModal } from './file-viewer';

const mocks = vi.hoisted(() => ({
    openStream: vi.fn(),
    closeMedia: vi.fn(),
    events: new Map<string, () => void>(),
    eventsOn: vi.fn(),
    enqueueDownload: vi.fn(),
    notify: vi.fn(),
}));

vi.mock('../../api', () => ({ openStream: mocks.openStream, closeMedia: mocks.closeMedia }));
vi.mock('../../ui/viewers/pdf-frame', () => ({ pdfViewerFrameSrc: () => 'about:blank', isPdfFrameMessage: () => false }));
vi.mock('../transfers', () => ({ enqueueDownload: mocks.enqueueDownload }));
vi.mock('../notifications', () => ({ notify: mocks.notify }));
vi.mock('../../../wailsjs/runtime/runtime', () => ({
    EventsOn: (name: string, callback: () => void) => {
        mocks.eventsOn(name, callback);
        mocks.events.set(name, callback);
        return vi.fn();
    },
}));

function opened(token: string, encrypted = true, name = 'secret.txt') {
    return {
        token,
        url: `http://127.0.0.1/media/${token}`,
        name,
        mimeType: 'text/plain',
        info: { encrypted, plaintextSize: 6, storedSize: 6 },
    };
}

function deferred<T>() {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((done) => {
        resolve = done;
    });
    return { promise, resolve };
}

function lock() {
    mocks.events.get('encrypted_media_sessions_closed')?.();
}

let host: HTMLElement;

beforeAll(() => {
    host = document.createElement('div');
    host.id = 'viewer-modal';
    document.body.appendChild(host);
    setupFileViewerModal();
});

beforeEach(() => {
    mocks.openStream.mockReset();
    mocks.closeMedia.mockReset().mockResolvedValue(undefined);
    vi.stubGlobal('fetch', vi.fn(async () => new Response('secret', { status: 200 })));
});

afterEach(() => {
    closeFileViewer();
    flushSync();
    vi.unstubAllGlobals();
});

describe('encrypted file viewer lifecycle', () => {
    it('rejects unsupported file types and identifies supported formats', async () => {
        expect(canOpenFileViewer('a.pdf')).toBe(true);
        expect(canOpenFileViewer('a.zip')).toBe(false);
        await openFileViewer({ id: 1, name: 'a.zip', size: 1 });
        expect(mocks.openStream).not.toHaveBeenCalled();
        expect(mocks.notify).toHaveBeenCalled();
    });

    it('reports stream errors without restoring a closed viewer', async () => {
        mocks.openStream.mockRejectedValueOnce(new Error('stream failed'));
        await openFileViewer({ id: 1, name: 'a.txt', size: 1 });
        expect(get(fileViewerState)).toMatchObject({ loading: false, error: 'Error: stream failed' });
        mocks.openStream.mockRejectedValueOnce('');
        await openFileViewer({ id: 1, name: 'a.txt', size: 1 });
        expect(get(fileViewerState).error).toBe('Could not open file');
        const pending = deferred<ReturnType<typeof opened>>();
        mocks.openStream.mockReturnValue(pending.promise.then(() => {
            throw new Error('late failure');
        }));
        const opening = openFileViewer({ id: 1, name: 'a.txt', size: 1, encrypted: true });
        await Promise.resolve();
        lock();
        pending.resolve(opened('unused'));
        await opening;
        expect(get(fileViewerState)).toMatchObject({ open: false, error: '' });
    });

    it('keeps a pending plain stream and its download target across lock', async () => {
        const pending = deferred<ReturnType<typeof opened>>();
        mocks.openStream.mockReturnValue(pending.promise);
        const opening = openFileViewer({ id: 4, name: 'plain.txt', size: 10 });
        await Promise.resolve();
        lock();
        pending.resolve({
            ...opened('plain', false, ''),
            info: { encrypted: false, plaintextSize: 0, storedSize: 0 },
        });
        await opening;
        flushSync();
        expect(get(fileViewerState)).toMatchObject({ open: true, title: 'plain.txt' });
        host.querySelector<HTMLButtonElement>('[aria-label="Download"]')?.click();
        expect(mocks.enqueueDownload).toHaveBeenCalledWith(4, 'plain.txt', 10);
    });

    it('registers the lock listener only once', () => {
        setupFileViewerModal();
        expect(mocks.eventsOn.mock.calls.filter(([name]) => name === 'encrypted_media_sessions_closed')).toHaveLength(1);
    });

    it('clears loaded decrypted text even when session cleanup fails', async () => {
        mocks.openStream.mockResolvedValue(opened('text'));
        await openFileViewer({ id: 1, name: 'secret.txt', size: 6, encrypted: true });
        await vi.waitFor(() => {
            flushSync();
            expect(host.querySelector('.text-viewer-body')?.textContent).toBe('secret');
        });
        mocks.closeMedia.mockRejectedValue(new Error('cleanup failed'));
        lock();
        flushSync();
        expect(get(fileViewerState)).toMatchObject({ open: false, url: '', token: '', kind: null });
        expect(host.querySelector('.text-viewer-body')).toBeNull();
        expect(mocks.closeMedia).toHaveBeenCalledWith('text');
    });

    it.each(['secret.pdf', 'secret.mp3'])('removes the encrypted media element for %s', async (name) => {
        mocks.openStream.mockResolvedValue(opened('media', true, name));
        await openFileViewer({ id: 1, name, size: 6, encrypted: true });
        flushSync();
        expect(host.querySelector('iframe, audio')).not.toBeNull();
        lock();
        flushSync();
        expect(host.querySelector('iframe, audio')).toBeNull();
    });

    it('uses authoritative encryption metadata and preserves plain viewers', async () => {
        mocks.openStream.mockResolvedValueOnce(opened('encrypted')).mockResolvedValueOnce(opened('plain', false));
        await openFileViewer({ id: 1, name: 'secret.txt', size: 6 });
        lock();
        expect(get(fileViewerState).open).toBe(false);
        await openFileViewer({ id: 2, name: 'plain.txt', size: 6 });
        lock();
        expect(get(fileViewerState)).toMatchObject({ open: true, token: 'plain' });
        expect(mocks.closeMedia).not.toHaveBeenCalledWith('plain');
    });

    it.each([true, undefined])('rejects an encrypted stream completing after lock (hint: %s)', async (encrypted) => {
        const pending = deferred<ReturnType<typeof opened>>();
        mocks.openStream.mockReturnValue(pending.promise);
        const opening = openFileViewer({ id: 1, name: 'secret.txt', size: 6, encrypted });
        await vi.waitFor(() => expect(mocks.openStream).toHaveBeenCalled());
        lock();
        pending.resolve(opened('late'));
        await opening;
        expect(get(fileViewerState).open).toBe(false);
        expect(mocks.closeMedia).toHaveBeenCalledWith('late');
    });

    it('invalidates an encrypted open while the prior session is closing', async () => {
        mocks.openStream.mockResolvedValue(opened('plain', false));
        await openFileViewer({ id: 1, name: 'plain.txt', size: 6 });
        const closing = deferred<void>();
        mocks.closeMedia.mockReturnValue(closing.promise);
        const opening = openFileViewer({ id: 2, name: 'secret.txt', size: 6, encrypted: true });
        lock();
        closing.resolve();
        await opening;
        expect(mocks.openStream).toHaveBeenCalledTimes(1);
        expect(get(fileViewerState).open).toBe(false);
    });

    it('does not let an older open replace a newer viewer after delayed cleanup', async () => {
        mocks.openStream.mockResolvedValue(opened('first', false));
        await openFileViewer({ id: 1, name: 'first.txt', size: 6 });
        const closing = deferred<void>();
        mocks.closeMedia.mockReturnValue(closing.promise);
        const oldOpen = openFileViewer({ id: 2, name: 'old.txt', size: 6 });
        mocks.openStream.mockResolvedValue(opened('new', false, 'new.txt'));
        await openFileViewer({ id: 3, name: 'new.txt', size: 6 });
        closing.resolve();
        await oldOpen;
        expect(get(fileViewerState)).toMatchObject({ open: true, token: 'new', title: 'new.txt' });
        expect(mocks.openStream.mock.calls.map(([id]) => id)).toEqual([1, 3]);
    });
});
