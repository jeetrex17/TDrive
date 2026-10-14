import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import FailedUploadsModal from './FailedUploadsModal.svelte';
import { failedUploadsModal } from './failed-uploads-store';

let host: HTMLElement;
let component: ReturnType<typeof mount>;
const retry = vi.fn();

beforeEach(() => {
    retry.mockClear();
    host = document.createElement('div');
    host.id = 'failed-uploads-modal';
    document.body.appendChild(host);
    component = mount(FailedUploadsModal, { target: host, props: { onClose: () => failedUploadsModal.close(), onRetry: retry } });
});

afterEach(async () => {
    failedUploadsModal.close();
    flushSync();
    await unmount(component);
    host.remove();
});

function open(failures: { index: number; name: string; message: string; code: string; retryable: boolean }[]) {
    failedUploadsModal.open({ paths: [], failures, parentId: 'folder', encrypt: false, driveId: 1, driveTitle: 'Personal' });
    flushSync();
}

describe('failed upload review', () => {
    it('shows every error with bounded pages and distinguishes safe retry from uncertain acceptance', () => {
        open(Array.from({ length: 22 }, (_, index) => ({ index, name: `file-${index}.txt`, message: `error ${index}`, code: index === 21 ? 'outcome_unknown' : 'upload_failed', retryable: index !== 21 })));
        expect(host.querySelectorAll('li')).toHaveLength(20);
        const next = [...host.querySelectorAll('button')].find((button) => button.textContent === 'Next');
        next?.click();
        flushSync();
        expect(host.querySelectorAll('li')).toHaveLength(2);
        expect(host.textContent).toContain('file-21.txt');
        expect(host.textContent).toContain('Ready to retry');
        expect(host.textContent).toContain('Check the folder before uploading it again');
        const button = host.querySelector<HTMLButtonElement>('#retry-failed-uploads');
        expect(button?.textContent).toContain('(21)');
        button?.click();
        expect(retry).toHaveBeenCalledTimes(1);
    });

    it('disables fresh retries when outcomes are uncertain or owned by resumable recovery', () => {
        open([
            { index: 0, name: 'unknown.txt', message: 'lost receipt', code: 'outcome_unknown', retryable: false },
            { index: 1, name: 'movie.mkv', message: 'paused', code: 'resumable_upload', retryable: false },
        ]);
        expect(host.querySelector<HTMLButtonElement>('#retry-failed-uploads')?.disabled).toBe(true);
        expect(host.textContent).toContain('Resume controls in Transfers');
    });
});
