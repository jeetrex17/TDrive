<script lang="ts">
    import FolderIcon from '@lucide/svelte/icons/folder';
    import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';

    type FileStateKind = 'loading' | 'empty' | 'error';

    interface FileStateProps {
        kind: FileStateKind;
        title: string;
        body?: string;
        actionLabel?: string;
        onAction?: () => void;
        secondaryActionLabel?: string;
        onSecondaryAction?: () => void;
    }

    let {
        kind,
        title,
        body = '',
        actionLabel = '',
        onAction,
        secondaryActionLabel = '',
        onSecondaryAction,
    }: FileStateProps = $props();
</script>

<div class={`file-state is-${kind}`} role={kind === 'error' ? 'alert' : 'status'} aria-busy={kind === 'loading' ? 'true' : 'false'}>
    {#if kind === 'loading'}
            <div class="file-state-skeleton" aria-hidden="true">
                {#each [74, 56, 68, 48, 62] as width, index (width)}
                    <div class="file-state-skeleton-row" style={`--skeleton-delay: ${index * 65}ms`}>
                        <span class="file-state-skeleton-name">
                            <span class="file-state-skeleton-icon"></span>
                            <span class="file-state-skeleton-bar" style={`--skeleton-width: ${width}%`}></span>
                        </span>
                        <span class="file-state-skeleton-meta"></span>
                        <span class="file-state-skeleton-meta"></span>
                        <span class="file-state-skeleton-action"></span>
                    </div>
                {/each}
            </div>
        {:else}
            <div class="file-state-icon" aria-hidden="true"> {#if kind === 'empty'}
                <FolderIcon size={24} strokeWidth={2} aria-hidden="true" />
            {:else if kind === 'error'}
                <TriangleAlertIcon size={24} strokeWidth={2} aria-hidden="true" />
            {/if} </div>
        {/if}
    <div class:sr-only={kind === 'loading'} class="file-state-title">{title}</div>
    {#if body}
        <div class:sr-only={kind === 'loading'} class="file-state-body">{body}</div>
    {/if}
    {#if actionLabel && onAction}
        <div class="file-state-actions">
            <button class="secondary-btn" type="button" onclick={onAction}>{actionLabel}</button>
            {#if secondaryActionLabel && onSecondaryAction}
                <button class="secondary-btn" type="button" onclick={onSecondaryAction}>{secondaryActionLabel}</button>
            {/if}
        </div>
    {/if}
</div>
