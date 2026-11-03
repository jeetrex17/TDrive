import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import { get } from 'svelte/store';
import ActivityView from './ActivityView.svelte';
import { activityView, closeActivity, openActivity } from './activity-store';
import { historyEvents, notifPanelOpen } from './notif-store';
import { uploadActivities, uploadsPaused, type UploadActivity } from './upload-activity-store';
import { resumableUploads } from '../../modules/resumable-uploads';
import { resumableDownloads } from '../../modules/resumable-downloads';
import { failedUploadReports, type FailedUploadsPayload } from '../modals/failed-uploads-store';
import { sidebarState } from '../sidebar/sidebar-store';

const queue = vi.hoisted(() => ({ removeQueuedUpload: vi.fn() }));
const actions = vi.hoisted(() => ({ reviewFailedUploads: vi.fn(), switchActiveChannel: vi.fn(), setUploadsPaused: vi.fn(), cancelActivityFile: vi.fn() }));
vi.mock('../../modules/upload-queue', () => queue);
vi.mock('../../modules/transfers', () => ({ downloadRetryFor: () => undefined, reviewFailedUploads: actions.reviewFailedUploads, setUploadsPaused: actions.setUploadsPaused, cancelActivityFile: actions.cancelActivityFile }));
vi.mock('../../modules/channels', () => ({ switchActiveChannel: actions.switchActiveChannel }));

Object.defineProperty(HTMLElement.prototype, 'offsetParent', { configurable: true, get() { return this.parentElement; } });

let host: HTMLDivElement;
let app: ReturnType<typeof mount>;
const batch = (status: UploadActivity['status'] = 'queued'): UploadActivity => ({
    id: 'session-1', name: 'Photo selection', driveId: 8, driveTitle: 'Family',
    destination: 'Summer', encrypt: true, addedAt: 1_700_000_000_000,
    startedAt: 0, finishedAt: 0, status, fileCount: 121, files: Array.from({ length: 121 }, (_, index) => ({ index, name: `photo-${index + 1}.jpg`, status })),
});
async function settle(): Promise<void> { flushSync(); await tick(); await tick(); }
function chips(): string[] {
    return Array.from(host.querySelectorAll('.file-chips button')).map((chip) => {
        const count = chip.querySelector('span')?.textContent;
        return count ? `${chip.textContent!.slice(0, -count.length)} ${count}` : chip.textContent!;
    });
}
function button(text: string): HTMLButtonElement {
    const match = Array.from(host.querySelectorAll('button')).find((el) => el.textContent?.trim() === text);
    if (!match) throw new Error(`Missing button ${text}`);
    return match;
}
beforeEach(() => {
    historyEvents.set([]); uploadActivities.set([]); resumableUploads.set([]); resumableDownloads.set([]);
    host = document.createElement('div'); host.id = 'activity-modal'; document.body.appendChild(host);
    app = mount(ActivityView, { target: host }); flushSync();
});
afterEach(async () => {
    closeActivity(); flushSync(); await unmount(app); host.remove();
    historyEvents.set([]); uploadActivities.set([]); resumableUploads.set([]); resumableDownloads.set([]); failedUploadReports.set([]);
    sidebarState.update((state) => ({ ...state, activeChannelId: null }));
    vi.clearAllMocks();
});

describe('full activity view', () => {
    it('promotes a hover preview into a full workspace without toggling closed', async () => {
        notifPanelOpen.set(true);
        openActivity(); await settle();
        expect(get(activityView).open).toBe(true);
        expect(get(notifPanelOpen)).toBe(false);
        expect(host.querySelector('[role="dialog"]')?.getAttribute('aria-labelledby')).toBe('activity-title');
        expect(host.textContent).toContain('No transfers yet');
        openActivity(); await settle();
        expect(get(activityView).open).toBe(true);
    });

    it('owns Escape and restores the invoking button after closing', async () => {
        const invoker = document.createElement('button'); invoker.id = 'notif-bell'; document.body.appendChild(invoker); invoker.focus();
        openActivity(); await settle();
        expect(document.activeElement).toBe(host.querySelector('#activity-search'));
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true })); await settle();
        expect(get(activityView).open).toBe(false);
        expect(document.activeElement).toBe(invoker);
        invoker.remove();
    });

    it('retains the full selected batch, metadata and bounded file pages', async () => {
        uploadActivities.set([batch()]); openActivity({ selectedId: 'session-1' }); await settle();
        const details = host.querySelector('[aria-label="Details for Photo selection"]');
        expect(details?.textContent).toContain('Family');
        expect(details?.textContent).toContain('Summer');
        expect(details?.textContent).toContain('Encrypted');
        expect(details?.textContent).toContain('Queued · Starting soon');
        // Every file is waiting, so there is nothing to narrow the list by.
        expect(details?.querySelector('.file-chips')).toBeNull();
        expect(details?.querySelectorAll('[data-activity-file]')).toHaveLength(50);
        expect(details?.textContent).toContain('photo-50.jpg');
        expect(details?.textContent).not.toContain('photo-51.jpg');
        button('Next files').click(); await settle();
        expect(details?.querySelectorAll('[data-activity-file]')).toHaveLength(50);
        expect(details?.textContent).toContain('photo-51.jpg');
        expect(details?.textContent).not.toContain('photo-1.jpg');
        button('Remove from queue').click();
        expect(queue.removeQueuedUpload).toHaveBeenCalledWith('session-1');
    });

    it('reads active file progress from the bounded aggregate sample', async () => {
        uploadActivities.set([{ ...batch('active'), transferId: 'xfer:up:sample', files: [{ index: 0, name: 'sample.bin', status: 'active' }, { index: 1, name: 'next.bin', status: 'active' }], fileCount: 2 }]);
        historyEvents.set([{ kind: 'transfer', id: 'xfer:up:sample', name: 'Photo selection', direction: 'up', status: 'active', progress: 25, bytes: 250, total: 1000, speed: 10, startedAt: 1, finishedAt: 0, items: [{ key: '0', name: 'sample.bin', progress: 50, total: 500 }] }]);
        openActivity(); await settle();
        const rows = host.querySelectorAll('[data-activity-file]');
        expect(rows[0].querySelector('.file-status')?.textContent).toBe('50%');
        expect(rows[1].textContent).toContain('Uploading');
        expect(rows[1].textContent).not.toContain('%');
    });

    it('searches inside batches and filters attention separately from successful history', async () => {
        uploadActivities.set([batch('done'), { ...batch('failed'), id: 'failed', name: 'Documents', files: [{ index: 0, name: 'tax-return.pdf', status: 'failed', message: 'Connection closed' }], fileCount: 1 }]);
        openActivity(); await settle(); button('Needs attention').click(); await settle();
        expect(host.querySelector('[aria-label="View Documents"]')).not.toBeNull();
        expect(host.querySelector('[aria-label="View Photo selection"]')).toBeNull();
        button('All').click(); await settle();
        const search = host.querySelector<HTMLInputElement>('[aria-label="Search activity"]')!;
        search.value = 'tax-return'; search.dispatchEvent(new Event('input', { bubbles: true })); await settle();
        expect(host.querySelector('[aria-label="View Documents"]')).not.toBeNull();
        expect(host.querySelector('[aria-label="View Photo selection"]')).toBeNull();
    });

    it('shows complete notice text and reports clipboard failure instead of claiming copied', async () => {
        const body = 'Detailed failure '.repeat(80);
        historyEvents.set([{ kind: 'event', id: 'notice-1', title: 'Could not upload', body, level: 'error', ts: 1_700_000_000_000 }]);
        Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: vi.fn().mockRejectedValue(new Error('denied')) } });
        openActivity({ tab: 'notifications', selectedId: 'notice-1' }); await settle();
        expect(host.querySelector('.activity-message')?.textContent).toBe(body);
        button('Copy details').click(); await settle();
        expect(host.textContent).toContain('Could not copy');
        expect(host.textContent).not.toContain('Details copied');
    });

    it('says a batch for a closed drive is paused and offers to open that drive', async () => {
        sidebarState.update((state) => ({ ...state, activeChannelId: 1 }));
        uploadActivities.set([batch()]); openActivity({ selectedId: 'session-1' }); await settle();
        const details = host.querySelector('[aria-label="Details for Photo selection"]');
        expect(details?.textContent).toContain('Paused until Family is open');
        expect(host.querySelector('.activity-item .item-summary')?.textContent).toBe('Paused until Family is open · 121 files');
        button('Open Family').click();
        expect(actions.switchActiveChannel).toHaveBeenCalledWith(8);
    });

    it('counts the open drive queue ahead of a waiting batch', async () => {
        uploadActivities.set([{ ...batch(), id: 'earlier', name: 'Earlier', addedAt: 1 }, batch(), { ...batch('active'), id: 'running', name: 'Running' }]);
        openActivity({ selectedId: 'session-1' }); await settle();
        expect(host.querySelector('[aria-label="Details for Photo selection"]')?.textContent).toContain('Queued · 1 upload ahead');
        expect(Array.from(host.querySelectorAll('.activity-item .item-title')).map((title) => title.textContent)).toEqual(['Running', 'Earlier', 'Photo selection']);
    });

    it('moves a batch failure review into its detail and keeps unrelated reports listed', async () => {
        const report = (activityId?: string): FailedUploadsPayload => ({ paths: ['/a'], failures: [{ index: 0, name: 'a', message: 'x', code: 'not_sent', retryable: true }], parentId: '', encrypt: false, driveId: 8, driveTitle: 'Family', activityId });
        const linked = report('failed');
        failedUploadReports.set([linked, report()]);
        uploadActivities.set([{ ...batch('failed'), id: 'failed', name: 'Documents' }]);
        openActivity({ selectedId: 'failed' }); await settle();
        expect(host.querySelectorAll('.failed-upload-report')).toHaveLength(1);
        button('Review failed files').click();
        expect(actions.reviewFailedUploads).toHaveBeenCalledWith(linked);
    });

    it('confirms before clearing history', async () => {
        historyEvents.set([{ kind: 'event', id: 'notice-1', title: 'Done', body: '', level: 'info', ts: 1 }]);
        openActivity(); await settle();
        button('Clear history').click(); await settle();
        expect(get(historyEvents)).toHaveLength(1);
        button('Cancel').click(); await settle();
        button('Clear history').click(); await settle();
        button('Clear').click(); await settle();
        expect(get(historyEvents)).toEqual([]);
    });

    it('opens a batch from the bell by its aggregate row id', async () => {
        uploadActivities.set([{ ...batch('active'), transferId: 'xfer:up:session-1' }, { ...batch('done'), id: 'other', name: 'Other' }]);
        openActivity({ selectedId: 'xfer:up:session-1' }); await settle();
        expect(host.querySelector('[aria-label="Details for Photo selection"]')).not.toBeNull();
    });

    it('lists loose files, then each folder with its own files and counts, and folds a folder', async () => {
        uploadActivities.set([{ ...batch('active'), fileCount: 6, files: [
            { index: 0, name: 'cover.jpg', path: 'cover.jpg', status: 'done' },
            { index: 1, name: 'a.jpg', path: 'Album/2024/a.jpg', status: 'done' },
            { index: 2, name: 'b.jpg', path: 'Album/2024/b.jpg', status: 'failed', message: 'Lost' },
            { index: 3, name: 'c.jpg', path: 'Album/c.jpg', status: 'active', progress: 40 },
        ] }]);
        openActivity({ selectedId: 'session-1' }); await settle();
        const list = () => Array.from(host.querySelectorAll('.file-list > li')).map((row) => row.classList.contains('file-folder')
            ? `[${row.querySelector('.folder-path')?.textContent}: ${row.querySelector('.folder-counts')?.textContent}]`
            : row.querySelector('.file-name')?.textContent);
        expect(list()).toEqual([
            'cover.jpg',
            '[Album / 2024: 2 files · 1 done · 1 failed]', 'a.jpg', 'b.jpg',
            '[Album: 1 file · 1 uploading]', 'c.jpg',
        ]);
        // Files the import has not reached yet count as waiting.
        expect(chips()).toEqual(['All', 'Uploading 1', 'Waiting 2', 'Failed 1', 'Done 2']);
        expect(Array.from(host.querySelectorAll('[data-activity-file]')).find((row) => row.textContent?.includes('c.jpg'))?.querySelector('.file-status')?.textContent).toBe('40%');
        const folder = Array.from(host.querySelectorAll<HTMLButtonElement>('.file-folder button')).find((row) => row.textContent?.includes('Album / 2024'));
        expect(folder?.getAttribute('aria-expanded')).toBe('true');
        folder?.click(); await settle();
        expect(list()).toEqual(['cover.jpg', '[Album / 2024: 2 files · 1 done · 1 failed]', '[Album: 1 file · 1 uploading]', 'c.jpg']);
        folder?.click(); await settle();
        const chip = (label: string) => Array.from(host.querySelectorAll<HTMLButtonElement>('.file-chips button')).find((item) => item.textContent?.startsWith(label))!;
        chip('Failed').click(); await settle();
        expect(chip('Failed').getAttribute('aria-pressed')).toBe('true');
        expect(list()).toEqual(['[Album / 2024: 1 file · 1 failed]', 'b.jpg']);
        chip('All').click(); await settle();
        expect(list()).toHaveLength(6);
    });

    it('offers pause and cancel for the running upload and for each file that has not finished', async () => {
        uploadActivities.set([{ ...batch('active'), fileCount: 4, files: [
            { index: 0, name: 'sending.jpg', status: 'active', progress: 40 },
            { index: 1, name: 'saving.jpg', status: 'active', progress: 100 },
            { index: 2, name: 'waiting.jpg', status: 'queued' },
            { index: 3, name: 'done.jpg', status: 'done' },
        ] }]);
        openActivity({ selectedId: 'session-1' }); await settle();
        const row = (name: string) => Array.from(host.querySelectorAll('[data-activity-file]')).find((item) => item.querySelector('.file-name')?.textContent === name);
        expect(row('sending.jpg')?.querySelector('.file-status')?.textContent).toBe('40%');
        expect(row('saving.jpg')?.querySelector('.file-status')?.textContent).toBe('Saving…');
        expect(row('waiting.jpg')?.querySelector('.file-status')?.textContent).toBe('Waiting');
        expect(chips()).toEqual(['All', 'Uploading 2', 'Waiting 1', 'Done 1']);
        // Only files that can still be stopped carry a cancel control.
        expect(Array.from(host.querySelectorAll('.file-cancel')).map((control) => control.getAttribute('aria-label'))).toEqual(['Cancel sending.jpg', 'Cancel waiting.jpg']);
        (row('waiting.jpg')?.querySelector('.file-cancel') as HTMLButtonElement).click();
        expect(actions.cancelActivityFile).toHaveBeenCalledWith('session-1', expect.objectContaining({ index: 2 }));

        button('Pause').click();
        expect(actions.setUploadsPaused).toHaveBeenCalledWith(true);
        uploadsPaused.set(true); await settle();
        expect(host.querySelector('.activity-details h3')?.textContent).toBe('Upload to Family paused');
        expect(host.querySelector('.detail-status')?.textContent).toBe('Files already sending will finish. The rest wait until you resume.');
        expect(row('waiting.jpg')?.querySelector('.file-status')?.textContent).toBe('Paused');
        expect(host.querySelector('.activity-item .item-summary')?.textContent).toContain('Paused');
        button('Resume').click();
        expect(actions.setUploadsPaused).toHaveBeenCalledWith(false);
        uploadsPaused.set(false);
    });

    it('keeps the session batch authoritative over its compact legacy row', async () => {
        uploadActivities.set([{ ...batch('active'), transferId: 'xfer:up:session-1' }]);
        historyEvents.set([{ kind: 'transfer', id: 'xfer:up:session-1', name: 'Photo selection', direction: 'up', status: 'active', progress: 30, bytes: 300, total: 1000, speed: 10, startedAt: 1, finishedAt: 0 }]);
        openActivity(); await settle();
        expect(host.querySelectorAll('[aria-label="View Photo selection"]')).toHaveLength(1);
        expect(host.textContent).toContain('300 of 1000 B');
    });
});
