<script lang="ts">
    import { playbackProgress, playbackResumeKey } from '../../modules/video/playback-resume';
    import { fileTypeFamily, fileTypeIcon } from './file-type';
    import { registerFileThumbnail, unregisterFileThumbnail } from './file-thumbnail-controller';
    import type { FileThumbnailIdentity } from './types';
    import type { ThumbnailPatch, ThumbnailStatus } from '../renditions/thumbnail-controller';

    interface Props {
        ext: string;
        identity: FileThumbnailIdentity;
    }

    let { ext, identity }: Props = $props();
    let status = $state<ThumbnailStatus>('idle');
    let src = $state('');
    let title = $state('');

    const family = $derived(fileTypeFamily(ext));
    const resumeKey = $derived(family === 'video' ? playbackResumeKey(identity.accountId ?? 0, identity) : null);
    const watched = $derived(resumeKey ? $playbackProgress.get(resumeKey) ?? 0 : 0);
    const TypeIcon = $derived(fileTypeIcon(family));

    function apply(patch: ThumbnailPatch): void {
        if (patch.status !== undefined) status = patch.status;
        if (patch.src !== undefined) src = patch.src;
        if (patch.title !== undefined) title = patch.title;
    }

    function register(node: HTMLElement, initial: FileThumbnailIdentity) {
        let current = initial;
        registerFileThumbnail(node, { ...current, apply });
        return {
            update(next: FileThumbnailIdentity) {
                if (next.accountId === current.accountId
                    && next.channelId === current.channelId
                    && next.fileId === current.fileId
                    && next.revision === current.revision) return;
                current = next;
                status = 'idle';
                src = '';
                title = '';
                registerFileThumbnail(node, { ...current, apply });
            },
            destroy() {
                unregisterFileThumbnail(node);
            },
        };
    }
</script>

<span
    class:row-thumbnail-loaded={status === 'loaded'}
    class="file-type-icon row-thumbnail"
    data-family={family}
    data-status={status}
    title={title || undefined}
    aria-hidden="true"
    use:register={identity}
>
    <TypeIcon size={20} strokeWidth={1.75} aria-hidden="true" />
    <img
        class="row-thumbnail-image"
        alt=""
        width="256"
        height="256"
        decoding="async"
        src={src || undefined}
    />
    {#if watched > 0}
        <span class="row-watch-progress" aria-hidden="true">
            <span class="row-watch-progress-fill" style:width={`${watched * 100}%`}></span>
        </span>
    {/if}
</span>

<style>
    .row-watch-progress {
        position: absolute;
        inset: auto 2px 2px;
        z-index: 3;
        height: 4px;
        overflow: hidden;
        border-radius: 2px;
        background: rgb(0 0 0 / 65%);
        box-shadow: 0 0 0 1px rgb(0 0 0 / 70%);
        pointer-events: none;
    }
    .row-watch-progress-fill {
        display: block;
        height: 100%;
        background: var(--accent);
    }
</style>
