// Which cancel a row gets is a property of the queue, not of the row, so it is
// decided in the tab and asserted here.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';

const cancelSingleUpload = vi.hoisted(() => vi.fn());
const cancelTransfersInDirection = vi.hoisted(() => vi.fn());
const resumeActions = vi.hoisted(() => ({
    resumeUpload: vi.fn(),
    chooseSourceAndResume: vi.fn(),
    pauseUpload: vi.fn(async () => {}),
    checkUploadStatus: vi.fn(async () => {}),
    discardUpload: vi.fn(async () => {}),
}));
vi.mock('../../modules/notif-bell', async (importOriginal) => {
    const actual = await importOriginal<typeof import('../../modules/notif-bell')>();
    return { ...actual, cancelSingleUpload, cancelTransfersInDirection };
});
vi.mock('../../modules/resumable-uploads', async (importOriginal) => {
    const actual = await importOriginal<typeof import('../../modules/resumable-uploads')>();
    return { ...actual, ...resumeActions };
});

import TransfersTab from './TransfersTab.svelte';
import { historyEvents, type TransferEvent } from '../notifications/notif-store';
import { resumableUploads } from '../../modules/resumable-uploads';

let host: HTMLElement;
let app: Record<string, unknown> | null = null;

function transfer(overrides: Partial<TransferEvent> = {}): TransferEvent {
    return {
        kind: 'transfer',
        id: 'xfer:up:1',
        direction: 'up',
        name: 'clip.mp4',
        progress: 40,
        total: 1000,
        bytes: 400,
        speed: 0,
        status: 'active',
        startedAt: 0,
        finishedAt: 0,
        ...overrides,
    };
}

function render(queue: TransferEvent[]): void {
    historyEvents.set(queue);
    app = mount(TransfersTab, { target: host, props: {} });
    flushSync();
}

function clickStop(name: string): void {
    const button = host.querySelector<HTMLButtonElement>(`button[aria-label="Stop ${name}"]`);
    if (!button) throw new Error(`No stop control for ${name}`);
    button.click();
}

beforeEach(() => {
    cancelSingleUpload.mockClear();
    cancelTransfersInDirection.mockClear();
    Object.values(resumeActions).forEach((action) => action.mockClear());
    resumableUploads.set([]);
    host = document.createElement('div');
    document.body.append(host);
});

afterEach(async () => {
    if (app) await unmount(app);
    app = null;
    historyEvents.set([]);
    resumableUploads.set([]);
    host.remove();
});

describe('durable large uploads', () => {
    const job = {
        jobId: 'job-7', channelId: 1, name: 'archive.zip', size: 3000,
        confirmedBytes: 1000, status: 'paused' as const, error: '',
    };

    it('shows backend confirmed bytes and hides the linked legacy row', () => {
        resumableUploads.set([job]);
        render([transfer({ resumableJobId: job.jobId, name: job.name })]);

        expect(host.querySelectorAll('.resumable-row')).toHaveLength(1);
        expect(host.querySelectorAll('.row:not(.resumable-row)')).toHaveLength(0);
        expect(host.querySelector('[role="progressbar"]')?.getAttribute('aria-valuenow')).toBe('33');
        expect(host.textContent).toContain('confirmed');
    });

    it('resumes and pauses a durable job from the mobile tab', () => {
        resumableUploads.set([job]);
        render([]);
        host.querySelector<HTMLButtonElement>('button[aria-label="Resume archive.zip"]')?.click();
        expect(resumeActions.resumeUpload).toHaveBeenCalledExactlyOnceWith(job.jobId);

        resumableUploads.set([{ ...job, status: 'uploading' }]);
        flushSync();
        host.querySelector<HTMLButtonElement>('button[aria-label="Pause archive.zip"]')?.click();
        expect(resumeActions.pauseUpload).toHaveBeenCalledExactlyOnceWith(job.jobId);
    });

    it('offers a retry while offline and excludes that wait from Stop all', () => {
        resumableUploads.set([{ ...job, status: 'waiting_network' }]);
        render([]);
        expect(host.textContent).toContain('Waiting for connection');
        expect(host.querySelector('button.transfers-clear')).toBeNull();
        host.querySelector<HTMLButtonElement>('button[aria-label="Resume archive.zip"]')?.click();
        expect(resumeActions.resumeUpload).toHaveBeenCalledExactlyOnceWith(job.jobId);
    });

    it('asks before discarding server parts', () => {
        resumableUploads.set([job]);
        render([]);
        host.querySelector<HTMLButtonElement>('button[aria-label="Discard archive.zip"]')?.click();
        flushSync();
        expect(resumeActions.discardUpload).not.toHaveBeenCalled();
        host.querySelector<HTMLButtonElement>('button[aria-label="Keep archive.zip"]')?.click();
        flushSync();
        expect(resumeActions.discardUpload).not.toHaveBeenCalled();
        host.querySelector<HTMLButtonElement>('button[aria-label="Discard archive.zip"]')?.click();
        flushSync();
        host.querySelector<HTMLButtonElement>('button[aria-label="Discard parts for archive.zip"]')?.click();
        expect(resumeActions.discardUpload).toHaveBeenCalledExactlyOnceWith(job.jobId);
    });

    it('offers source recovery, uncertain manifest checking and restart guidance', () => {
        resumableUploads.set([{ ...job, status: 'needs_source' }]);
        render([]);
        host.querySelector<HTMLButtonElement>('button[aria-label="Choose original file for archive.zip"]')?.click();
        expect(resumeActions.chooseSourceAndResume).toHaveBeenCalledExactlyOnceWith(job.jobId);

        resumableUploads.set([{ ...job, status: 'uncertain_manifest' }]);
        flushSync();
        expect(host.querySelector('button[aria-label="Discard archive.zip"]')).toBeNull();
        host.querySelector<HTMLButtonElement>('button[aria-label="Check and retry archive.zip"]')?.click();
        expect(resumeActions.checkUploadStatus).toHaveBeenCalledExactlyOnceWith(job.jobId);

        resumableUploads.set([{ ...job, status: 'restart_required' }]);
        flushSync();
        expect(host.querySelector('button[aria-label="Resume archive.zip"]')).toBeNull();
        expect(host.textContent).toContain('start a new upload');
    });
});

describe('stopping one transfer', () => {
    it('stops just that upload, because the tab also offers Cancel all', () => {
        render([transfer({ id: 'xfer:up:1' })]);
        clickStop('clip.mp4');
        expect(cancelSingleUpload).toHaveBeenCalledExactlyOnceWith(1);
        expect(cancelTransfersInDirection).not.toHaveBeenCalled();
    });

    it('falls back to the whole direction for an import, which has no per-file id', () => {
        render([transfer({ id: 'xfer:up:import' })]);
        clickStop('clip.mp4');
        expect(cancelTransfersInDirection).toHaveBeenCalledExactlyOnceWith('up');
        expect(cancelSingleUpload).not.toHaveBeenCalled();
    });

    it('stops the download that is actually running', () => {
        render([transfer({ id: 'xfer:down:file:42', direction: 'down', name: 'plan.pdf' })]);
        clickStop('plan.pdf');
        expect(cancelTransfersInDirection).toHaveBeenCalledExactlyOnceWith('down');
    });

    it('does not pretend a waiting download can be stopped on its own', () => {
        render([transfer({ id: 'xfer:down:file:42', direction: 'down', name: 'plan.pdf', status: 'queued' })]);
        expect(host.querySelector('button[aria-label="Stop plan.pdf"]')).toBeNull();
    });
});

describe('stopping the lot', () => {
    it('is offered only once there is more than one transfer to stop', () => {
        render([transfer()]);
        expect(host.querySelector('button.transfers-clear')).toBeNull();
    });

    it('stops both directions, since a queue can be waiting in either', () => {
        render([
            transfer({ id: 'xfer:up:1' }),
            transfer({ id: 'xfer:down:file:42', direction: 'down', name: 'plan.pdf', status: 'queued' }),
        ]);
        const button = host.querySelector<HTMLButtonElement>('button.transfers-clear');
        expect(button?.textContent?.trim()).toBe('Stop all');
        button?.click();
        expect(cancelTransfersInDirection).toHaveBeenCalledTimes(2);
        expect(cancelTransfersInDirection).toHaveBeenCalledWith('up');
        expect(cancelTransfersInDirection).toHaveBeenCalledWith('down');
    });

    it('pauses durable uploads as well as stopping legacy transfers', () => {
        resumableUploads.set([{
            jobId: 'job-7', channelId: 1, name: 'archive.zip', size: 3000,
            confirmedBytes: 1000, status: 'uploading', error: '',
        }]);
        render([transfer({ id: 'xfer:down:file:42', direction: 'down', name: 'plan.pdf' })]);
        host.querySelector<HTMLButtonElement>('button.transfers-clear')?.click();
        expect(resumeActions.pauseUpload).toHaveBeenCalledExactlyOnceWith('job-7');
        expect(cancelTransfersInDirection).toHaveBeenCalledExactlyOnceWith('down');
    });
});

describe('a download that has not worked out its size yet', () => {
    it('can still be stopped: it is the job the backend has in hand', () => {
        render([transfer({
            id: 'xfer:down:file:42', direction: 'down', name: 'plan.pdf',
            status: 'active', progress: 0, total: 0, bytes: 0,
        })]);
        expect(host.textContent).toContain('Preparing');
        clickStop('plan.pdf');
        expect(cancelTransfersInDirection).toHaveBeenCalledExactlyOnceWith('down');
    });
});
