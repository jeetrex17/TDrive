<script lang="ts">
    import { onMount } from 'svelte';
    import tdriveLogo from '../../assets/images/tdrive-logo.png';
    import { formatAppErrorDiagnostic, type AppError } from '../../modules/errors';
    import Button from '../Button.svelte';

    interface Props {
        error: AppError;
        /** Absent when there is nothing to restore, as after a failed startup. */
        onRecover?: () => void;
        onReload: () => void;
        assurance?: string;
    }

    let {
        error,
        onRecover,
        onReload,
        assurance = 'Restore the screen first. If the problem returns, reload TDrive; an in-progress action may need to be started again.',
    }: Props = $props();
    let heading: HTMLHeadingElement;
    const diagnostic = $derived(formatAppErrorDiagnostic(error));

    onMount(() => heading.focus());
</script>

<!-- The same calm column as signing in: the app's mark, what went wrong in a
     sentence, what is safe, and the one or two moves left. The diagnostic is
     there for a bug report, folded away until someone asks for it. -->
<main class="error-recovery" role="alert" aria-labelledby="error-recovery-title">
    <section class="recovery-column">
        <img class="recovery-mark" src={tdriveLogo} alt="" width="66" height="42" />
        <h1 id="error-recovery-title" tabindex="-1" bind:this={heading}>{error.title}</h1>
        <p class="recovery-message">{error.message}</p>
        <p class="recovery-assurance">{assurance}</p>

        <div class="recovery-actions">
            {#if onRecover}
                <Button onclick={onRecover}>Restore screen</Button>
                <Button variant="secondary" onclick={onReload}>Reload TDrive</Button>
            {:else}
                <Button onclick={onReload}>Reload TDrive</Button>
            {/if}
        </div>

        <details class="recovery-details">
            <summary>Technical details</summary>
            <pre>{diagnostic}</pre>
        </details>
    </section>
</main>

<style>
    .error-recovery {
        min-height: 100vh;
        min-height: 100dvh;
        display: grid;
        place-items: center;
        padding: var(--space-6) var(--space-4) calc(var(--space-6) + 8vh);
        color: var(--color-text);
        background: var(--color-canvas);
    }

    .recovery-column {
        width: min(400px, 100%);
        display: grid;
        justify-items: center;
        text-align: center;
    }

    .recovery-mark {
        width: 66px;
        height: auto;
        margin-bottom: var(--space-6);
    }

    h1 {
        color: var(--color-text);
        font-size: var(--type-2xl);
        font-weight: var(--weight-semibold);
        line-height: 1.2;
        letter-spacing: -0.02em;
        text-wrap: balance;
        outline: none;
    }

    /* Focused on arrival so a screen reader starts here; it is not a control,
       so it draws no ring. */
    .recovery-message {
        margin-top: var(--space-2);
        color: var(--color-text-soft);
        font-size: var(--type-base);
        line-height: 1.5;
        text-wrap: pretty;
    }

    .recovery-assurance {
        margin-top: var(--space-2);
        color: var(--color-text-muted);
        font-size: var(--type-sm);
        line-height: 1.5;
        text-wrap: pretty;
    }

    .recovery-actions {
        display: grid;
        grid-auto-flow: column;
        grid-auto-columns: 1fr;
        gap: var(--space-2);
        width: 100%;
        margin-top: var(--space-6);
    }

    .recovery-details {
        width: 100%;
        margin-top: var(--space-4);
        color: var(--color-text-subtle);
        font-size: var(--type-xs);
    }

    .recovery-details summary {
        width: fit-content;
        margin: 0 auto;
        padding: var(--space-1) var(--space-2);
        border-radius: var(--radius-xs);
        cursor: pointer;
    }

    .recovery-details summary:focus-visible {
        outline: none;
        box-shadow: var(--focus-ring);
    }

    .recovery-details pre {
        max-height: 180px;
        overflow: auto;
        margin-top: var(--space-2);
        padding: var(--space-3);
        border: 1px solid var(--color-border-soft);
        border-radius: var(--radius-md);
        color: var(--color-text-muted);
        background: var(--color-surface-1);
        font: 0.72rem/1.5 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
        text-align: left;
        overflow-wrap: anywhere;
        white-space: pre-wrap;
        user-select: text;
    }

    @media (max-width: 420px) {
        .recovery-actions { grid-auto-flow: row; }
    }
</style>
