import { writable } from 'svelte/store';
import type { ResumableUpload } from '../api/resumable-uploads';

/** Backend journal state shared by the desktop bell and mobile Transfers tab. */
export const resumableUploads = writable<ResumableUpload[]>([]);
