import { writable } from 'svelte/store';

/** A read-only source view, intentionally independent from the active drive. */
export const channelSurfaceOpen = writable(false);

export function openChannelSurface(): void { channelSurfaceOpen.set(true); }
export function closeChannelSurface(): void { channelSurfaceOpen.set(false); }
