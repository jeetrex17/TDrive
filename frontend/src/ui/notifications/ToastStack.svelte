<script lang="ts">
    import CheckIcon from '@lucide/svelte/icons/check';
    import CircleXIcon from '@lucide/svelte/icons/circle-x';
    import InfoIcon from '@lucide/svelte/icons/info';
    import LoaderCircleIcon from '@lucide/svelte/icons/loader-circle';
    import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';
    import XIcon from '@lucide/svelte/icons/x';
    import { toasts, type ToastItem } from './toast-store';

    interface Props {
        onDismiss: (id: string) => void;
        onPauseToast: (id: string) => void;
        onResumeToast: (id: string) => void;
        onPauseAll: () => void;
        onResumeAll: () => void;
    }

    let { onDismiss, onPauseToast, onResumeToast, onPauseAll, onResumeAll }: Props = $props();

    // Mirrors the legacy .toast-leaving exit: fade and slide while the stack
    // collapses underneath.
    function toastOut(_node: Element) {
        return {
            duration: 180,
            css: (t: number) => `opacity: ${t}; transform: translateY(${(1 - t) * 6}px);`,
        };
    }

    function onToastClick(event: MouseEvent, toast: ToastItem): void {
        if ((event.target as HTMLElement).closest('.toast-close')) return;
        // Click anywhere on an error to clear it quickly.
        if (toast.level === 'error') onDismiss(toast.id);
    }
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
            out:toastOut
            onmouseenter={() => onPauseToast(toast.id)}
            onmouseleave={() => onResumeToast(toast.id)}
            onclick={(event) => onToastClick(event, toast)}
        >
            <span class="toast-icon" aria-hidden="true">
                {#if toast.spinner}
                    <LoaderCircleIcon class="toast-spinner" size={16} strokeWidth={2.4} aria-hidden="true" />
                {:else if toast.level === 'success'}
                    <CheckIcon size={16} strokeWidth={2} aria-hidden="true" />
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
