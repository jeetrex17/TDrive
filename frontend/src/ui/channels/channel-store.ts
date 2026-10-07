import { derived, writable } from 'svelte/store';
import { sourceKey, type ChannelSource } from './channel-model';

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
/** Full peer identity so two peer kinds or accounts can never collide. */
export const openChannelKey = writable<string | null>(null);

export const openChannel = derived(
    [channelSources, openChannelKey],
    ([$sources, $key]) => $sources.sources.find((source) => sourceKey(source) === $key) ?? null,
);

export const channelPickerOpen = writable(false);

export function closeChannel(): void {
    openChannelKey.set(null);
}
