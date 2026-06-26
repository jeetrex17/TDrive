<script lang="ts">
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
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M3 7a2 2 0 012-2h5l2 2h7a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2V7z" />
            </svg>
        {:else if kind === 'error'}
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v4m0 4h.01M10.3 4.3L2.8 17.2A2 2 0 004.5 20h15a2 2 0 001.7-2.8L13.7 4.3a2 2 0 00-3.4 0z" />
            </svg>
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
