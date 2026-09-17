<script lang="ts">
    import { onMount } from 'svelte';
    import { getGalleryStorage, clearGalleryCache, type GalleryStorage } from '../../api/gallery-storage';
    import { formatBytes } from '../../utils';

    let usage = $state<GalleryStorage | null>(null);
    let busy = $state(false);
    let error = $state('');
    let notice = $state('');
    let alive = true;

    async function refresh(): Promise<void> {
        busy = true;
        error = '';
        try {
            const result = await getGalleryStorage();
            if (alive) usage = result;
        } catch {
            if (alive) error = 'Storage information is unavailable. Try again.';
        } finally { if (alive) busy = false; }
    }

    async function clear(): Promise<void> {
        if (busy) return;
        busy = true;
        error = '';
        notice = '';
        try {
            const result = await clearGalleryCache();
            if (alive) { usage = result; notice = 'Photo cache cleared.'; }
        } catch {
            if (alive) error = 'Some cached photos could not be cleared. Try again.';
        } finally { if (alive) busy = false; }
    }

    onMount(() => { void refresh(); return () => { alive = false; }; });
</script>

<section class="photo-cache-panel" aria-label="Local photo storage" aria-busy={busy}>
    <div class="photo-cache-row"><span>Photo cache</span><strong>{usage ? `${formatBytes(usage.cacheBytes)} / ${formatBytes(usage.cacheLimit)}` : 'Calculating…'}</strong></div>
    <div class="photo-cache-row"><span>Local catalog</span><span>{usage ? formatBytes(usage.catalogBytes) : 'Calculating…'}</span></div>
    <p>Older cached photos are removed automatically. Clearing the cache keeps your originals, catalog, and saved downloads.</p>
    <button data-clear-photo-cache type="button" class="secondary-btn" disabled={busy || !usage} onclick={() => void clear()}>{busy && usage ? 'Clearing…' : 'Clear photo cache'}</button>
    {#if error}<p role="alert">{error}</p>{#if !usage}<button type="button" class="secondary-btn" disabled={busy} onclick={() => void refresh()}>Retry</button>{/if}{/if}
    {#if notice}<p role="status">{notice}</p>{/if}
</section>

<style>
    .photo-cache-panel { padding: 16px; display: grid; gap: 12px; }
    .photo-cache-row { display: flex; justify-content: space-between; align-items: baseline; gap: 12px; font-size: 13px; }
    .photo-cache-row strong { font-weight: 500; font-variant-numeric: tabular-nums; }
    p { margin: 0; color: var(--text-secondary); font-size: 12px; line-height: 1.5; }
    button { min-height: 44px; }
</style>
