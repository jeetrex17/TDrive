<script lang="ts">
    import { playbackProgress, playbackResumeKey } from '../../modules/video/playback-resume';
    import type { FileThumbnailIdentity } from './types';

    let { identity }: { identity: FileThumbnailIdentity } = $props();
    const resumeKey = $derived(playbackResumeKey(identity.accountId ?? 0, identity));
    const watched = $derived(resumeKey ? $playbackProgress.get(resumeKey) ?? 0 : 0);
</script>

{#if watched > 0}
    <span class="row-watch-progress" role="meter" aria-label="Watched" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(watched * 100)}>
        <span class="row-watch-progress-fill" style:width={`${watched * 100}%`}></span>
    </span>
{/if}

<style>
    .row-watch-progress {
        display: block;
        width: min(96px, 100%);
        height: 4px;
        margin-top: 3px;
        overflow: hidden;
        border-radius: 2px;
        background: color-mix(in srgb, var(--accent) 20%, transparent);
    }
    .row-watch-progress-fill {
        display: block;
        height: 100%;
        border-radius: inherit;
        background: var(--accent);
    }
</style>
