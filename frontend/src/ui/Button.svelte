<script lang="ts">
    import type { Snippet } from 'svelte';
    import type { HTMLButtonAttributes } from 'svelte/elements';

    type ButtonVariant = 'primary' | 'secondary';
    type ButtonSize = 'sm' | 'md';

    type ButtonProps = Omit<HTMLButtonAttributes, 'children'> & {
        variant?: ButtonVariant;
        size?: ButtonSize;
        children?: Snippet;
    };

    let {
        variant = 'primary',
        size = 'md',
        disabled = false,
        type = 'button',
        class: className = '',
        children,
        ...rest
    }: ButtonProps = $props();
</script>

<button
    {...rest}
    class={`ui-button ${className}`.trim()}
    class:is-primary={variant === 'primary'}
    class:is-secondary={variant === 'secondary'}
    class:is-small={size === 'sm'}
    {type}
    {disabled}
>
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
        font-weight: var(--weight-strong);
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
        color: var(--color-on-action);
        background: var(--color-action);
        border-color: var(--color-action);
    }

    .is-primary:hover:not(:disabled) {
        background: var(--color-action-hover);
        border-color: var(--color-action-hover);
    }

    .is-secondary {
        color: var(--color-text);
        background: var(--color-surface-1);
        border-color: var(--color-border-soft);
    }

    .is-secondary:hover:not(:disabled) {
        background: var(--color-surface-2);
    }

    .is-small {
        min-height: 32px;
        padding: 0 var(--space-3);
        font-size: var(--font-size-xs);
    }

    .ui-button:active:not(:disabled) {
        transform: translateY(0.5px) scale(0.985);
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

    /* Phones: a 44px target without changing the desktop control. */
    :global(html.mobile) .ui-button {
        min-height: 44px;
        padding: 0 var(--space-5);
        font-size: var(--font-size-base);
    }

    :global(html.mobile) .is-small {
        min-height: 40px;
        padding: 0 var(--space-4);
    }

    @media (prefers-reduced-motion: reduce) {
        .ui-button {
            transition: none;
        }

        .ui-button:active:not(:disabled) {
            transform: none;
        }
    }
</style>
