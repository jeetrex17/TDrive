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
    }

    let {
        kind,
        title,
        body = '',
        actionLabel = '',
        onAction,
    }: FileStateProps = $props();
</script>

<div class={`file-state is-${kind}`} role={kind === 'error' ? 'alert' : 'status'} aria-busy={kind === 'loading' ? 'true' : 'false'}>
    <div class="file-state-icon" aria-hidden="true">
        {#if kind === 'empty'}
            <FolderIcon size={24} strokeWidth={2} aria-hidden="true" />
        {:else if kind === 'error'}
            <TriangleAlertIcon size={24} strokeWidth={2} aria-hidden="true" />
        {/if}
    </div>
    <div class="file-state-title">{title}</div>
    {#if body}
        <div class="file-state-body">{body}</div>
    {/if}
    {#if actionLabel && onAction}
        <div class="file-state-actions">
            <button class="secondary-btn" type="button" onclick={onAction}>{actionLabel}</button>
        </div>
    {/if}
</div>
