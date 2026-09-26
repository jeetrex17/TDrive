<script lang="ts">
    import { onMount } from 'svelte';
    import { formatAppErrorDiagnostic, toAppError } from '../../modules/errors';
    import { authScreenActions } from '../../modules/auth';
    import { isMobilePlatform } from '../../api';
    import AppShell from '../AppShell.svelte';
    import MobileShell from '../mobile/MobileShell.svelte';
    import AuthScreens from '../auth/AuthScreens.svelte';
    import Button from '../Button.svelte';
    import ErrorBoundary from '../errors/ErrorBoundary.svelte';
    import CloudAlertIcon from '@lucide/svelte/icons/cloud-alert';
    import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';
    import AppSkeleton from './AppSkeleton.svelte';
    import { appView, showFatalView, type AppLifecycle } from './app-store';
    interface Props {
        lifecycle: AppLifecycle;
    }

    let { lifecycle }: Props = $props();

    // Choose the shell the first time the view leaves startup and never flip
    // after: on iOS the platform is only known once the gateway hydrates, which
    // lands before the auth or dashboard view shows, so this reads true by then.
    // The cache latch is a plain memo, so desktop resolves synchronously with no
    // flash and mobile stays mobile for the session.
    let frozenShell: 'mobile' | 'desktop' | null = null;
    const shell = $derived.by((): 'mobile' | 'desktop' | null => {
        if (frozenShell) return frozenShell;
        if ($appView.kind === 'startup') return null;
        frozenShell = isMobilePlatform() ? 'mobile' : 'desktop';
        return frozenShell;
    });

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
    <div class="app-state-screen app-state-startup">
        <AppSkeleton label="Starting TDrive" />
    </div>
{:else if $appView.kind === 'fatal'}
    <div class="app-state-screen">
        <!-- The same panel the recoverable boundary shows, minus the recovery:
             startup latches after one attempt, so reloading is the only move
             left and offering anything else would be a dead button. -->
        <section class="fatal-panel" role="alert" aria-labelledby="fatal-title">
            <div class="fatal-signal" aria-hidden="true"></div>

            <div class="fatal-icon" aria-hidden="true">
                <CloudAlertIcon size={28} strokeWidth={1.8} />
            </div>

            <div class="fatal-content">
                <h1 id="fatal-title" tabindex="-1">{$appView.error.title}</h1>
                <p class="fatal-message">{$appView.error.message}</p>
                <p class="fatal-assurance">
                    Nothing on Telegram has changed. Reload TDrive; if it keeps failing, the details
                    below say where startup stopped.
                </p>

                <div class="fatal-actions">
                    <Button onclick={reload}>
                        <RefreshCwIcon size={16} strokeWidth={2.2} aria-hidden="true" />
                        Reload TDrive
                    </Button>
                </div>

                <details class="fatal-details">
                    <summary>Technical details</summary>
                    <pre>{formatAppErrorDiagnostic($appView.error)}</pre>
                </details>
            </div>
        </section>
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

            {#if shell === 'mobile'}
                <MobileShell dashboardVisible={$appView.kind === 'dashboard'} />
            {:else}
                <AppShell dashboardVisible={$appView.kind === 'dashboard'} />
            {/if}

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
        z-index: var(--z-app-state);
        display: grid;
        place-items: center;
        min-width: 0;
        min-height: 0;
        padding: var(--space-5);
        background: var(--bg-dark);
    }

    /* Always worn together with .app-state-screen, so it inherits that rung
       and only adds the scrim. */
    .app-state-overlay {
        background: var(--scrim);
        backdrop-filter: blur(4px);
    }

    .app-state-startup { padding: 0; }

    /* Deliberately shaped like .recovery-panel in ErrorFallback: the two
       full-screen failures are one surface to the reader, and the
       unrecoverable one must not read as the smaller event. */
    .fatal-panel {
        width: min(680px, 100%);
        display: grid;
        grid-template-columns: 6px 64px minmax(0, 1fr);
        align-items: start;
        gap: var(--space-5);
        padding: clamp(var(--space-5), 5vw, 44px);
        border: 1px solid var(--border);
        border-radius: var(--radius-lg);
        background: var(--color-surface-0);
        box-shadow: var(--shadow-md);
        text-align: left;
    }

    .fatal-signal {
        align-self: stretch;
        min-height: 168px;
        border-radius: var(--radius-pill);
        background: var(--danger);
    }

    .fatal-icon {
        width: 56px;
        height: 56px;
        display: grid;
        place-items: center;
        border: 1px solid var(--overlay-danger-strong);
        border-radius: var(--radius-lg);
        color: var(--danger);
        background: var(--overlay-danger-soft);
    }

    .fatal-content { min-width: 0; }

    h1 {
        margin: 0;
        color: var(--color-text);
        font-size: clamp(1.45rem, 4vw, 1.9rem);
        font-weight: var(--weight-strong);
        line-height: 1.15;
        letter-spacing: -0.025em;
    }

    .fatal-message {
        max-width: 56ch;
        margin: var(--space-3) 0 0;
        color: var(--text-main);
        font-size: var(--type-md);
        line-height: 1.55;
    }

    .fatal-assurance {
        max-width: 62ch;
        margin: var(--space-3) 0 0;
        color: var(--text-muted);
        font-size: var(--type-sm);
        line-height: 1.55;
    }

    .fatal-actions { margin-top: var(--space-6); }

    .fatal-details {
        margin-top: var(--space-5);
        color: var(--text-muted);
        font-size: var(--type-xs);
    }

    .fatal-details summary {
        width: fit-content;
        padding: var(--space-1) 0;
        cursor: pointer;
    }

    .fatal-details summary:focus-visible {
        outline: none;
        border-radius: var(--radius-xs);
        box-shadow: var(--focus-ring);
    }

    .fatal-details pre {
        max-height: 180px;
        overflow: auto;
        margin: var(--space-2) 0 0;
        padding: var(--space-3);
        border: 1px solid var(--border);
        border-radius: var(--radius-sm);
        background: var(--color-surface-1);
        font: 0.72rem/1.5 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
        overflow-wrap: anywhere;
        white-space: pre-wrap;
        user-select: text;
    }

    @media (max-width: 560px) {
        .fatal-panel {
            grid-template-columns: 5px minmax(0, 1fr);
            gap: var(--space-4);
        }

        .fatal-icon { display: none; }
    }
</style>
