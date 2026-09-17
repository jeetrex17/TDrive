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

    /**
     * A finger gets the whole toast as its dismiss target rather than the 
     * close button alone, because a notice that has been read is in the way,
     * and aiming at a small × above the tab bar to say so is work. A pointer
     * keeps the button: there, the toast may hold a link or a Retry, and a
     * click that swallows the surface would take those with it.
     */
    function tapToDismiss(event: PointerEvent, id: string): void {
        if (event.pointerType !== 'touch') return;
        if ((event.target as HTMLElement | null)?.closest('[data-toast-interactive]')) return;
        onDismiss(id);
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
            class:has-mobile-detail={toast.level === 'error' && Boolean(toast.body)}
            data-id={toast.id}
            role={toast.level === 'error' ? 'alert' : 'status'}
            aria-describedby={toast.level === 'error' && toast.body ? `toast-detail-${toast.id}` : undefined}
            onpointerenter={(event) => { if (hovering(event)) onPauseToast(toast.id); }}
            onpointerleave={(event) => { if (hovering(event)) onResumeToast(toast.id); }}
            onpointerup={(event) => tapToDismiss(event, toast.id)}
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
    /* Informational notices stay compact. Errors retain one concise detail
       line so the failure is understandable without opening another surface. */
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
    :global(html.mobile .toast.toast-error.has-mobile-detail) { align-items: flex-start; }
    :global(html.mobile .toast.toast-error.has-mobile-detail .toast-content) {
        flex-direction: column;
        align-items: stretch;
        gap: 2px;
        padding-block: 2px;
    }
    :global(html.mobile .toast.toast-error.has-mobile-detail .toast-title) {
        flex: 0 1 auto;
        width: 100%;
    }
    :global(html.mobile .toast.toast-error.has-mobile-detail .toast-body) {
        display: block;
        overflow: hidden;
        color: var(--color-text-subtle);
        font-size: var(--text-xs);
        line-height: 1.3;
        text-overflow: ellipsis;
        white-space: nowrap;
    }
</style>
