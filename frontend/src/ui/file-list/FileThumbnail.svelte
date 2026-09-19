<script lang="ts">
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
                if (next.channelId === current.channelId
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
</span>
