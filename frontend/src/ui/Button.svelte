<script lang="ts">
    import type { Snippet } from 'svelte';
    import type { HTMLButtonAttributes } from 'svelte/elements';

    type ButtonVariant = 'primary' | 'secondary' | 'danger';
    type ButtonSize = 'sm' | 'md';

    type ButtonProps = Omit<HTMLButtonAttributes, 'children'> & {
        variant?: ButtonVariant;
        size?: ButtonSize;
        loading?: boolean;
        children?: Snippet;
    };

    let {
        variant = 'primary',
        size = 'md',
        loading = false,
        disabled = false,
        type = 'button',
        class: className = '',
        children,
        ...rest
    }: ButtonProps = $props();

    const isDisabled = $derived(disabled || loading);
</script>

<button
    {...rest}
    class={`ui-button ${className}`.trim()}
    class:is-primary={variant === 'primary'}
    class:is-secondary={variant === 'secondary'}
    class:is-danger={variant === 'danger'}
    class:is-small={size === 'sm'}
    {type}
    disabled={isDisabled}
    aria-busy={loading ? 'true' : undefined}
>
    {#if loading}
        <span class="ui-button-spinner" aria-hidden="true"></span>
    {/if}
    <span class="ui-button-content">
        {@render children?.()}
    </span>
</button>

<style>
    .ui-button {
        min-height: 40px;
        display: inline-flex;
        align-items: center;
        justify-content: center;
        gap: var(--space-2);
        border: 1px solid transparent;
        border-radius: var(--radius-md);
        padding: 0 var(--space-4);
        color: var(--text-main);
        background: transparent;
        font: inherit;
        font-size: var(--font-size-sm);
        font-weight: 800;
        line-height: 1;
        cursor: pointer;
        user-select: none;
        transition:
            background-color var(--motion-med) var(--ease-standard),
            border-color var(--motion-med) var(--ease-standard),
            color var(--motion-med) var(--ease-standard),
            transform var(--motion-fast) var(--ease-standard),
            opacity var(--motion-med) var(--ease-standard);
    }

    .ui-button-content {
        display: inline-flex;
        align-items: center;
        justify-content: center;
        gap: var(--space-2);
        min-width: 0;
    }

    .is-primary {
        color: var(--color-surface-0);
        background: var(--accent);
        border-color: var(--accent);
    }

    .is-primary:hover:not(:disabled) {
        background: var(--accent-hover);
        border-color: var(--accent-hover);
        transform: translateY(-1px);
    }

    .is-secondary {
        color: var(--text-main);
        background: transparent;
        border-color: var(--border);
    }

    .is-secondary:hover:not(:disabled) {
        background: var(--surface-control);
        border-color: var(--color-surface-3);
    }

    .is-danger {
        color: var(--color-surface-0);
        background: var(--danger);
        border-color: var(--danger);
    }

    .is-danger:hover:not(:disabled) {
        background: color-mix(in srgb, var(--danger) 88%, white);
        border-color: color-mix(in srgb, var(--danger) 88%, white);
        transform: translateY(-1px);
    }

    .is-small {
        min-height: 32px;
        padding: 0 var(--space-3);
        font-size: var(--font-size-xs);
    }

    .ui-button:active:not(:disabled) {
        transform: translateY(0);
    }

    .ui-button:focus-visible {
        outline: none;
        box-shadow: var(--focus-ring);
    }

    .ui-button:disabled {
        cursor: not-allowed;
        opacity: 0.55;
        transform: none;
    }

    .ui-button-spinner {
        width: 14px;
        height: 14px;
        border-radius: var(--radius-pill);
        border: 2px solid currentColor;
        border-top-color: transparent;
        animation: ui-button-spin 720ms linear infinite;
    }

    @keyframes ui-button-spin {
        to { transform: rotate(360deg); }
    }

    @media (prefers-reduced-motion: reduce) {
        .ui-button {
            transition: none;
        }

        .ui-button-spinner {
            animation: none;
        }
    }
</style>
