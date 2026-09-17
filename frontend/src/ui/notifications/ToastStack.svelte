<script lang="ts">
    import CheckIcon from '@lucide/svelte/icons/check';
    import CircleXIcon from '@lucide/svelte/icons/circle-x';
    import InfoIcon from '@lucide/svelte/icons/info';
    import LoaderCircleIcon from '@lucide/svelte/icons/loader-circle';
    import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';
    import XIcon from '@lucide/svelte/icons/x';
    import { toasts } from './toast-store';
    import { swipeDismiss } from './swipe-dismiss';

    interface Props {
        onDismiss: (id: string) => void;
        onPauseToast: (id: string) => void;
        onResumeToast: (id: string) => void;
        onPauseAll: () => void;
        onResumeAll: () => void;
    }

    let { onDismiss, onPauseToast, onResumeToast, onPauseAll, onResumeAll }: Props = $props();

    /**
     * Hovering holds a toast open so it can be read. A finger cannot hover, and
     * a touch host says so badly: tapping fires mouseenter with no matching
     * mouseleave until the next tap somewhere else, which on a phone leaves the
     * countdown frozen and the toast on screen for good. The stack sits right
     * above the tab bar, so that tap happens constantly.
     *
     * Pointer events carry the answer with them, so the pause is taken only
     * from something that can really hover and really leave.
     */
    function hovering(event: PointerEvent): boolean {
        return event.pointerType !== 'touch';
    }
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div
    class="toast-stack-inner"
    style="display: contents;"
    onpointerenter={(event) => { if (hovering(event)) onPauseAll(); }}
    onpointerleave={(event) => { if (hovering(event)) onResumeAll(); }}
>
    {#each $toasts as toast (toast.id)}
        <div
            class={`toast toast-${toast.level}`}
            data-id={toast.id}
            role={toast.level === 'error' ? 'alert' : 'status'}
            aria-describedby={toast.body ? `toast-detail-${toast.id}` : undefined}
            onpointerenter={(event) => { if (hovering(event)) onPauseToast(toast.id); }}
            onpointerleave={(event) => { if (hovering(event)) onResumeToast(toast.id); }}
            use:swipeDismiss={{ onDismiss: () => onDismiss(toast.id) }}
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
                    <div id={`toast-detail-${toast.id}`} class="toast-body">{toast.body}</div>
                {/if}
                {#if toast.action}
                    <button
                        class="toast-action toast-interactive-control"
                        type="button"
                        data-toast-interactive
                        onclick={(event) => {
                            event.stopPropagation();
                            toast.action?.run();
                        }}
                    >{toast.action.label}</button>
                {/if}
            </div>
            <button
                class="toast-close toast-interactive-control"
                type="button"
                aria-label="Dismiss"
                data-toast-interactive
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
