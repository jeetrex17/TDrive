<script lang="ts">
    import { onMount } from 'svelte';
    import { formatAppErrorDiagnostic, toAppError } from '../../modules/errors';
    import { authScreenActions } from '../../modules/auth';
    import AppShell from '../AppShell.svelte';
    import AuthScreens from '../auth/AuthScreens.svelte';
    import Button from '../Button.svelte';
    import ErrorBoundary from '../errors/ErrorBoundary.svelte';
    import StateView from '../StateView.svelte';
    import AppSkeleton from './AppSkeleton.svelte';
    import { appView, showFatalView, type AppLifecycle } from './app-store';
    interface Props {
        lifecycle: AppLifecycle;
    }

    let { lifecycle }: Props = $props();

    onMount(() => {
        let mounted = true;
        let stopped = false;
        const stop = () => {
            if (stopped) return;
            stopped = true;
            lifecycle.stop();
        };

        void lifecycle.start().catch((cause: unknown) => {
            if (!mounted) return;
            const error = toAppError(cause, { source: 'startup' });
            console.error(`Application startup failed\n${formatAppErrorDiagnostic(error)}`);
            stop();
            showFatalView(error);
        });

        return () => {
            mounted = false;
            stop();
        };
    });

    function reload(): void {
        location.reload();
    }
</script>

{#if $appView.kind === 'startup'}
    <div class="app-state-screen">
        <AppSkeleton label="Starting TDrive" />
    </div>
{:else if $appView.kind === 'fatal'}
    <div class="app-state-screen">
        <StateView tone="error" title={$appView.error.title} body={$appView.error.message}>
                <Button onclick={reload}>Reload TDrive</Button>
        </StateView>
    </div>
{:else}
    <ErrorBoundary>
            <div
                id="auth-wrapper"
                hidden={$appView.kind !== 'auth'}
                aria-hidden={$appView.kind === 'auth' ? undefined : 'true'}
            >
                <AuthScreens {...authScreenActions} />
            </div>

            <AppShell dashboardVisible={$appView.kind === 'dashboard'} />

            {#if $appView.kind === 'loading'}
                <div class="app-state-screen app-state-overlay">
                    <AppSkeleton label={$appView.message} compact />
                </div>
            {/if}
    </ErrorBoundary>
{/if}

<style>
    #auth-wrapper[hidden] {
        display: none;
    }

    .app-state-screen {
        position: fixed;
        inset: 0;
        z-index: 10000;
        display: grid;
        place-items: center;
        min-width: 0;
        min-height: 0;
        padding: var(--space-5);
        background: var(--bg-dark);
    }

    .app-state-overlay {
        z-index: 9999;
    }
</style>
