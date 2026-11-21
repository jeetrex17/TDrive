<script lang="ts">
    type ProgressBarProps = {
        value?: number;
        max?: number;
        label?: string;
        indeterminate?: boolean;
    };

    let {
        value = 0,
        max = 100,
        label = '',
        indeterminate = false,
    }: ProgressBarProps = $props();

    const normalizedMax = $derived(Math.max(1, max));
    const clampedValue = $derived(Math.min(Math.max(0, value), normalizedMax));
    const percent = $derived(Math.round((clampedValue / normalizedMax) * 100));
</script>

<div class="ui-progress" class:is-indeterminate={indeterminate}>
    <div
        class="ui-progress-track"
        role="progressbar"
        aria-label={label || undefined}
        aria-valuemin={indeterminate ? undefined : 0}
        aria-valuemax={indeterminate ? undefined : normalizedMax}
        aria-valuenow={indeterminate ? undefined : clampedValue}
    >
        <div class="ui-progress-fill" style={`width: ${indeterminate ? 36 : percent}%`}></div>
    </div>
</div>

<style>
    .ui-progress {
        width: 100%;
        display: grid;
        gap: var(--space-2);
        color: var(--text-muted);
        font-size: var(--font-size-xs);
        line-height: 1.25;
    }

    .ui-progress-track {
        position: relative;
        height: 6px;
        overflow: hidden;
        border-radius: var(--radius-pill);
        background: var(--overlay-white-2);
    }

    .ui-progress-fill {
        height: 100%;
        border-radius: inherit;
        background: var(--accent);
        transition: width var(--motion-med) var(--ease-standard);
    }

    .is-indeterminate .ui-progress-fill {
        position: absolute;
        left: -36%;
        animation: ui-progress-indeterminate 1200ms var(--ease-standard) infinite;
    }

    @keyframes ui-progress-indeterminate {
        to { left: 100%; }
    }

    @media (prefers-reduced-motion: reduce) {
        .ui-progress-fill {
            transition: none;
        }

        .is-indeterminate .ui-progress-fill {
            left: 0;
            animation: none;
        }
    }
</style>
