<script lang="ts">
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
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" class="toast-spinner"><circle cx="12" cy="12" r="9" stroke-opacity="0.25"/><path d="M12 3 a9 9 0 0 1 9 9"/></svg>
                {:else if toast.level === 'success'}
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="20 6 9 17 4 12"/></svg>
                {:else if toast.level === 'warning'}
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>
                {:else if toast.level === 'error'}
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="15" y1="9" x2="9" y2="15"/><line x1="9" y1="9" x2="15" y2="15"/></svg>
                {:else}
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/></svg>
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
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            </button>
        </div>
    {/each}
</div>
