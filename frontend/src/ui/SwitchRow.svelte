<script lang="ts">
    /**
     * A settings row whose whole surface is the switch. The row is the
     * control rather than a label beside one, so a thumb-sized tap target on a
     * phone becomes a full-width one, and the accessible name is the title
     * itself -- `getByRole('switch', { name })` finds it with no wiring.
     */
    type SwitchRowProps = {
        title: string;
        description?: string;
        checked: boolean;
        disabled?: boolean;
        onchange: (checked: boolean) => void;
    };

    let { title, description = '', checked, disabled = false, onchange }: SwitchRowProps = $props();

    const id = $props.id();
    const titleId = `${id}-title`;
    const descriptionId = `${id}-description`;
</script>

<button
    class="ui-switch-row"
    type="button"
    role="switch"
    aria-checked={checked}
    aria-labelledby={titleId}
    aria-describedby={description ? descriptionId : undefined}
    {disabled}
    onclick={() => onchange(!checked)}
>
    <span class="ui-switch-copy">
        <span id={titleId} class="ui-switch-title">{title}</span>
        {#if description}
            <span id={descriptionId} class="ui-switch-description">{description}</span>
        {/if}
    </span>
    <span class="ui-switch" aria-hidden="true">
        <span class="ui-switch-thumb"></span>
    </span>
</button>

<style>
    .ui-switch-row {
        display: flex;
        align-items: center;
        gap: var(--space-4);
        width: 100%;
        min-height: 40px;
        padding: var(--space-2) 0;
        border: 0;
        background: transparent;
        color: var(--text-main);
        font: inherit;
        text-align: left;
        cursor: pointer;
    }

    .ui-switch-row:disabled {
        cursor: not-allowed;
    }

    .ui-switch-row:disabled .ui-switch-copy {
        opacity: 0.55;
    }

    .ui-switch-row:focus-visible {
        outline: none;
        border-radius: var(--radius-sm);
        box-shadow: var(--focus-ring);
    }

    .ui-switch-copy {
        flex: 1 1 auto;
        min-width: 0;
        display: grid;
        gap: 2px;
    }

    .ui-switch-title {
        font-size: var(--font-size-sm);
        font-weight: var(--weight-semibold);
        line-height: 1.3;
    }

    .ui-switch-description {
        color: var(--text-muted);
        font-size: var(--font-size-xs);
        line-height: 1.45;
    }

    /* The thumb stays white on both appearances: it reads as a physical
       control against the accent and against the neutral track alike. */
    .ui-switch {
        flex: 0 0 auto;
        display: block;
        width: 36px;
        height: 22px;
        padding: 2px;
        box-sizing: border-box;
        border-radius: var(--radius-pill);
        background: var(--color-surface-3);
        transition: background var(--motion-fast) var(--ease-standard);
    }

    .ui-switch-row[aria-checked='true'] .ui-switch {
        background: var(--accent);
    }

    .ui-switch-row:disabled .ui-switch {
        opacity: 0.55;
    }

    .ui-switch-thumb {
        display: block;
        width: 18px;
        height: 18px;
        border-radius: 50%;
        background: #fff;
        box-shadow: 0 1px 3px rgba(0, 0, 0, 0.28);
        transform: translateX(0);
        transition: transform var(--motion-med) var(--ease-standard);
    }

    .ui-switch-row[aria-checked='true'] .ui-switch-thumb {
        transform: translateX(14px);
    }

    @media (prefers-reduced-motion: reduce) {
        .ui-switch,
        .ui-switch-thumb {
            transition: none;
        }
    }

    /* Phone rows sit in a card at arm's length: taller, larger type. */
    :global(html.mobile) .ui-switch-row {
        min-height: 52px;
        padding: var(--space-2) 0;
    }

    :global(html.mobile) .ui-switch-title {
        font-size: var(--mobile-type-body);
        font-weight: var(--weight-medium);
    }

    :global(html.mobile) .ui-switch-description {
        font-size: var(--mobile-type-caption);
    }
</style>
