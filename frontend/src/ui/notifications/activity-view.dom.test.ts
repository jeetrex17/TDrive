import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import { get } from 'svelte/store';
import ActivityView from './ActivityView.svelte';
import { activityView, closeActivity, openActivity } from './activity-store';
import { historyEvents, notifPanelOpen } from './notif-store';
import { uploadActivities, type UploadActivity } from './upload-activity-store';
import { resumableUploads } from '../../modules/resumable-uploads';
import { resumableDownloads } from '../../modules/resumable-downloads';
import { failedUploadReports, type FailedUploadsPayload } from '../modals/failed-uploads-store';
import { sidebarState } from '../sidebar/sidebar-store';

const queue = vi.hoisted(() => ({ removeQueuedUpload: vi.fn() }));
const actions = vi.hoisted(() => ({ reviewFailedUploads: vi.fn(), switchActiveChannel: vi.fn() }));
vi.mock('../../modules/upload-queue', () => queue);
vi.mock('../../modules/transfers', () => ({ downloadRetryFor: () => undefined, reviewFailedUploads: actions.reviewFailedUploads }));
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
        expect(rows[0].textContent).toContain('Uploading · 50%');
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

    it('lists moving and failed files first and counts files an import has not reached as waiting', async () => {
        uploadActivities.set([{ ...batch('active'), fileCount: 5, files: [
            { index: 0, name: 'done.jpg', status: 'done' },
            { index: 1, name: 'moving.jpg', status: 'active', progress: 40 },
            { index: 2, name: 'broken.jpg', status: 'failed', message: 'Lost' },
        ] }]);
        openActivity({ selectedId: 'session-1' }); await settle();
        const rows = Array.from(host.querySelectorAll('[data-activity-file] .file-name')).map((row) => row.textContent);
        expect(rows).toEqual(['moving.jpg', 'broken.jpg', 'done.jpg']);
        expect(host.querySelector('[data-activity-file]')?.textContent).toContain('Uploading · 40%');
        expect(host.querySelector('.file-summary')?.textContent).toBe('1 completed · 1 failed · 1 uploading · 2 waiting');
    });

    it('keeps the session batch authoritative over its compact legacy row', async () => {
        uploadActivities.set([{ ...batch('active'), transferId: 'xfer:up:session-1' }]);
        historyEvents.set([{ kind: 'transfer', id: 'xfer:up:session-1', name: 'Photo selection', direction: 'up', status: 'active', progress: 30, bytes: 300, total: 1000, speed: 10, startedAt: 1, finishedAt: 0 }]);
        openActivity(); await settle();
        expect(host.querySelectorAll('[aria-label="View Photo selection"]')).toHaveLength(1);
        expect(host.textContent).toContain('300 of 1000 B');
    });
});
