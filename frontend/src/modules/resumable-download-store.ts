import { writable } from 'svelte/store';
import type { ResumableDownload } from '../api/resumable-downloads';

/** UI read model shared without importing the recovery controller lifecycle. */
export const resumableDownloads = writable<ResumableDownload[]>([]);
