<script lang="ts">
    import { onMount } from 'svelte';
    import { onRuntimeEvent } from '../../api';
    import { getGalleryPreparation, normalizeGalleryPreparation, startGalleryPreparation, stopGalleryPreparation, type GalleryPreparationStatus } from '../../api/gallery';
    import { callWithPasswordRetry } from '../../modules/modals/encryption-password';
    import { formatBytes } from '../../utils';
    import { selectedFileRowKeys } from '../file-list/row-state-store';
    import ModalShell from '../modals/ModalShell.svelte';
    import { rearmMissing } from './gallery-controller';

    let { channelId }: { channelId: number } = $props();
    let status = $state<GalleryPreparationStatus | null>(null);
    let review = $state(false);
    let busy = $state(false);
    let error = $state('');
    let remaining = $derived(Math.max(0, (status?.total ?? 0) - (status?.completed ?? 0) - (status?.skipped ?? 0)));
    let disposed = false;

    async function refresh(): Promise<void> {
        try {
            const next = await getGalleryPreparation();
            if (!disposed && next.channelId === channelId) status = next;
        } catch {
            // The gallery remains usable if the optional preparation estimate
            // is unavailable. Retain an explicit retry action for this feature.
            if (!disposed) error = 'Could not check preview availability.';
        }
    }

    async function start(): Promise<void> {
        busy = true;
        error = '';
        const targetChannel = channelId;
        try {
            const result = await callWithPasswordRetry(() => startGalleryPreparation(targetChannel));
            if (disposed || targetChannel !== channelId) return;
            if (!result.ok) { error = result.error.message; return; }
            review = false;
            await refresh();
        } catch { error = 'Could not prepare previews. Try again.'; }
        finally { busy = false; }
    }

    async function pause(): Promise<void> {
        busy = true;
        try {
            const result = await stopGalleryPreparation();
            if (!result.ok) error = result.error.message;
            await refresh();
        } catch { error = 'Could not pause preview preparation. Try again.'; }
        finally { busy = false; }
    }

    onMount(() => {
        void refresh();
        const unsubscribe = onRuntimeEvent('gallery_preparation_progress', (payload) => {
            const next = normalizeGalleryPreparation(payload);
            if (next.channelId !== channelId) return;
            const advanced = next.completed > (status?.completed ?? 0);
            status = next;
            if (advanced) rearmMissing();
        });
        return () => { disposed = true; unsubscribe(); };
    });
</script>

{#if $selectedFileRowKeys.size === 0 && (error || status?.running || remaining > 0 || (status?.skipped ?? 0) > 0)}
    <div class="gallery-preparation" aria-label="Photo previews">
        {#if status?.running}
            <span role="status">Preparing {(status.completed + (status.skipped ?? 0)).toLocaleString()} / {status.total.toLocaleString()}</span>
            <button class="secondary-btn gallery-prepare-pause" type="button" disabled={busy} onclick={pause}>Pause</button>
        {:else if remaining > 0 || error}
            <button class="secondary-btn gallery-prepare-action" type="button" onclick={() => { if (status) review = true; else void refresh(); }}>Create previews</button>
        {/if}
        {#if status?.skipped}<span role="status">{status.skipped.toLocaleString()} unsupported photos skipped</span>{/if}
        {#if !review && (error || status?.error)}<span class="gallery-preparation-error" role="status">{error || status?.error}</span>{/if}
    </div>
{/if}

<div id="gallery-preparation-modal" class="modal-overlay" style="display: none" aria-hidden="true">
    <ModalShell hostId="gallery-preparation-modal" open={review} title="Create photo previews?" titleId="gallery-preparation-title" restoreFocus=".gallery-prepare-action" initialFocus="#gallery-prepare-cancel" onClose={() => { if (!busy) review = false; }}>
        <p class="modal-subtitle">Create small previews for {remaining.toLocaleString()} photos. This will download about {formatBytes(Math.max(0, (status?.bytesTotal ?? 0) - (status?.bytesDone ?? 0)))} of originals and upload reusable previews to this drive.</p>
        <p class="modal-subtitle">You can pause and continue later. Encrypted photos keep their previews encrypted. Wi-Fi and a desktop are recommended for a large library.</p>
        {#if error}<p class="gallery-preparation-error" role="alert">{error}</p>{/if}
        {#snippet actions()}
            <button id="gallery-prepare-cancel" class="secondary-btn" type="button" disabled={busy} onclick={() => { review = false; }}>Cancel</button>
            <button id="gallery-prepare-confirm" class="primary-btn" type="button" disabled={busy} onclick={start}>{busy ? 'Starting…' : 'Create previews'}</button>
        {/snippet}
    </ModalShell>
</div>
