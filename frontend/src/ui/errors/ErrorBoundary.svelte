<script lang="ts">
    import { onDestroy, type Snippet } from 'svelte';
    import { toAppError } from '../../modules/errors';
    import { notifyAppError } from '../../modules/notifications';
    import ErrorFallback from './ErrorFallback.svelte';

    interface Props {
        children: Snippet;
    }

    let { children }: Props = $props();
    let reportQueued = false;
    let mounted = true;

    function reportRenderError(error: unknown): void {
        if (reportQueued) return;
        reportQueued = true;

        // Let Svelte remove the failed subtree before publishing to the toast
        // store. If ToastStack caused the fault, it cannot synchronously fault
        // this boundary again while the report is being written.
        queueMicrotask(() => {
            if (!mounted) return;
            notifyAppError(error, {
                id: 'render-boundary-error',
                title: 'Screen recovery needed',
                source: 'render',
            });
            queueMicrotask(() => {
                reportQueued = false;
            });
        });
    }


    function reload(): void {
        window.location.reload();
    }

    onDestroy(() => {
        mounted = false;
    });
</script>

{#snippet failed(error: unknown, reset: () => void)}
    {@const appError = toAppError(error, { source: 'render' })}
    <ErrorFallback error={appError} onRecover={reset} onReload={reload} />
{/snippet}

<svelte:boundary onerror={reportRenderError} {failed}>
    {@render children()}
</svelte:boundary>
