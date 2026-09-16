<script lang="ts">
    import CheckIcon from '@lucide/svelte/icons/check';
    import CircleXIcon from '@lucide/svelte/icons/circle-x';
    import InfoIcon from '@lucide/svelte/icons/info';
    import LoaderCircleIcon from '@lucide/svelte/icons/loader-circle';
    import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';
    import XIcon from '@lucide/svelte/icons/x';
    import { toasts } from './toast-store';

    interface Props {
        onDismiss: (id: string) => void;
        onPauseToast: (id: string) => void;
        onResumeToast: (id: string) => void;
        onPauseAll: () => void;
        onResumeAll: () => void;
    }

    let { onDismiss, onPauseToast, onResumeToast, onPauseAll, onResumeAll }: Props = $props();



</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div
    class="toast-stack-inner"
    style="display: contents;"
    onmouseenter={onPauseAll}
    onmouseleave={onResumeAll}
>
    {#each $toasts as toast (toast.id)}
        <div
            class={`toast toast-${toast.level}`}
            data-id={toast.id}
            role={toast.level === 'error' ? 'alert' : 'status'}
            onmouseenter={() => onPauseToast(toast.id)}
            onmouseleave={() => onResumeToast(toast.id)}
        >
            <span class="toast-icon" aria-hidden="true">
                {#if toast.spinner}
                    <LoaderCircleIcon class="toast-spinner" size={16} strokeWidth={2.4} aria-hidden="true" />
                {:else if toast.level === 'success'}
                    <CheckIcon size={16} strokeWidth={2.5} aria-hidden="true" />
                {:else if toast.level === 'warning'}
                    <TriangleAlertIcon size={16} strokeWidth={2} aria-hidden="true" />
                {:else if toast.level === 'error'}
                    <CircleXIcon size={16} strokeWidth={2} aria-hidden="true" />
                {:else}
                    <InfoIcon size={16} strokeWidth={2} aria-hidden="true" />
                {/if}
            </span>
            <div class="toast-content">
                <div class="toast-title">{toast.title}</div>
                {#if toast.body}
                    <div class="toast-body">{toast.body}</div>
                {/if}
                {#if toast.action}
                    <button
                        class="toast-action"
                        type="button"
                        onclick={(event) => {
                            event.stopPropagation();
                            toast.action?.run();
                        }}
                    >{toast.action.label}</button>
                {/if}
            </div>
            <button
                class="toast-close"
                type="button"
                aria-label="Dismiss"
                onclick={(event) => {
                    event.stopPropagation();
                    onDismiss(toast.id);
                }}
            >
                <XIcon size={16} strokeWidth={2} aria-hidden="true" />
            </button>
        </div>
    {/each}
</div>

<style>
    /* Mobile: the stack sits above the tab bar and safe area, spans the width
       with gutters, and toasts read as a single line. The module caps the
       queue at two on a phone. Opaque, no blur, matching the mobile bars. */
    :global(html.mobile .toast-stack) {
        left: var(--space-4);
        right: var(--space-4);
        bottom: calc(var(--tabbar-height, 49px) + var(--inset-bottom) + var(--space-3));
        width: auto;
        max-width: none;
    }
    :global(html.mobile .toast) {
        background: var(--color-surface-2);
        -webkit-backdrop-filter: none;
        backdrop-filter: none;
        align-items: center;
    }
    /* Single line: title and its action sit on one row, the multi-line body
       is dropped. */
    :global(html.mobile .toast-content) {
        flex-direction: row;
        align-items: center;
        gap: var(--space-2);
    }
    :global(html.mobile .toast-title) {
        flex: 1;
        min-width: 0;
        white-space: nowrap;
        overflow: hidden;
        text-overflow: ellipsis;
    }
    :global(html.mobile .toast-action) {
        flex: 0 0 auto;
        margin-top: 0;
        align-self: center;
    }
    :global(html.mobile .toast-body) { display: none; }
</style>
