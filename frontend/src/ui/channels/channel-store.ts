import { derived, writable } from 'svelte/store';
import type { ChannelSource } from './channel-model';

export type ChannelSourcesState =
    | { status: 'idle' | 'loading' | 'ready'; sources: readonly ChannelSource[] }
    | { status: 'error'; sources: readonly ChannelSource[]; error: unknown };

export const EMPTY_CHANNEL_SOURCES: ChannelSourcesState = { status: 'idle', sources: [] };

/** The channels this account has added to TDrive, in sidebar order. */
export const channelSources = writable<ChannelSourcesState>(EMPTY_CHANNEL_SOURCES);

/**
 * The channel the main area shows in place of the active drive. It never
 * changes the active drive, so leaving a channel lands back where you were.
 */
export const openChannelId = writable<number | null>(null);

export const openChannel = derived(
    [channelSources, openChannelId],
    ([$sources, $id]) => $sources.sources.find((source) => source.channelId === $id) ?? null,
);

export const channelPickerOpen = writable(false);

export function closeChannel(): void {
    openChannelId.set(null);
}
