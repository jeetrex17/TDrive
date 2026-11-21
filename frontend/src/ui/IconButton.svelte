<script lang="ts">
    import type { Snippet } from 'svelte';
    import type { HTMLButtonAttributes } from 'svelte/elements';

    type IconButtonSize = 'sm' | 'md';

    type IconButtonProps = Omit<HTMLButtonAttributes, 'children'> & {
        label: string;
        size?: IconButtonSize;
        children?: Snippet;
    };

    let {
        label,
        size = 'md',
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
    class:is-small={size === 'sm'}
    {type}
    {title}
    {disabled}
    aria-label={label}
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

    .is-small {
        width: 32px;
        height: 32px;
        border-radius: var(--radius-sm);
    }

    .is-small .ui-icon-button-content {
        width: 16px;
        height: 16px;
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

    /* Phones: the glyph and its box stay as they are; the hit area grows to
       44px through a pseudo-element (interactivity-3). */
    :global(html.mobile) .ui-icon-button {
        position: relative;
    }

    :global(html.mobile) .ui-icon-button::after {
        position: absolute;
        inset: -4px;
        content: '';
    }

    :global(html.mobile) .is-small::after {
        inset: -6px;
    }

    @media (prefers-reduced-motion: reduce) {
        .ui-icon-button {
            transition: none;
        }

        .ui-icon-button:active:not(:disabled) {
            transform: none;
        }
    }
</style>
