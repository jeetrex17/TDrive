<script lang="ts">
    import type { Snippet } from 'svelte';
    import type { HTMLButtonAttributes } from 'svelte/elements';

    type IconButtonTone = 'neutral' | 'accent' | 'danger';
    type IconButtonSize = 'sm' | 'md' | 'lg';

    type IconButtonProps = Omit<HTMLButtonAttributes, 'children'> & {
        label: string;
        tone?: IconButtonTone;
        size?: IconButtonSize;
        pressed?: boolean;
        children?: Snippet;
    };

    let {
        label,
        tone = 'neutral',
        size = 'md',
        pressed,
        disabled = false,
        type = 'button',
        title = label,
        class: className = '',
        children,
        ...rest
    }: IconButtonProps = $props();
</script>

<button
    {...rest}
    class={`ui-icon-button ${className}`.trim()}
    class:is-accent={tone === 'accent'}
    class:is-danger={tone === 'danger'}
    class:is-small={size === 'sm'}
    class:is-large={size === 'lg'}
    {type}
    {title}
    {disabled}
    aria-label={label}
    aria-pressed={pressed === undefined ? undefined : pressed ? 'true' : 'false'}
>
    <span class="ui-icon-button-content" aria-hidden="true">
        {@render children?.()}
    </span>
</button>

<style>
    .ui-icon-button {
        width: 36px;
        height: 36px;
        display: inline-grid;
        place-items: center;
        border: 1px solid var(--border);
        border-radius: var(--radius-md);
        color: var(--text-muted);
        background: transparent;
        font: inherit;
        cursor: pointer;
        user-select: none;
        transition:
            background-color var(--motion-med) var(--ease-standard),
            border-color var(--motion-med) var(--ease-standard),
            color var(--motion-med) var(--ease-standard),
            transform var(--motion-fast) var(--ease-standard),
            opacity var(--motion-med) var(--ease-standard);
    }

    .ui-icon-button-content {
        width: 18px;
        height: 18px;
        display: inline-grid;
        place-items: center;
    }

    .ui-icon-button :global(svg) {
        width: 100%;
        height: 100%;
        display: block;
    }

    .ui-icon-button:hover:not(:disabled) {
        color: var(--text-main);
        border-color: var(--color-surface-3);
        background: var(--surface-control);
    }

    .is-accent {
        color: var(--accent);
        border-color: color-mix(in srgb, var(--accent) 34%, transparent);
    }

    .is-accent:hover:not(:disabled),
    .is-accent[aria-pressed="true"] {
        color: var(--accent-hover);
        border-color: color-mix(in srgb, var(--accent) 52%, transparent);
        background: var(--overlay-accent-1);
    }

    .is-danger {
        color: var(--danger);
        border-color: var(--overlay-danger-2);
    }

    .is-danger:hover:not(:disabled) {
        background: var(--overlay-danger-1);
        border-color: color-mix(in srgb, var(--danger) 46%, transparent);
    }

    .is-small {
        width: 32px;
        height: 32px;
        border-radius: var(--radius-sm);
    }

    .is-small .ui-icon-button-content {
        width: 16px;
        height: 16px;
    }

    .is-large {
        width: 44px;
        height: 44px;
        border-radius: var(--radius-lg);
    }

    .is-large .ui-icon-button-content {
        width: 20px;
        height: 20px;
    }

    .ui-icon-button:active:not(:disabled) {
        transform: translateY(0.5px) scale(0.98);
    }

    .ui-icon-button:focus-visible {
        outline: none;
        box-shadow: var(--focus-ring);
    }

    .ui-icon-button:disabled {
        cursor: not-allowed;
        opacity: 0.5;
        transform: none;
    }

    @media (prefers-reduced-motion: reduce) {
        .ui-icon-button {
            transition: none;
        }
    }
</style>
