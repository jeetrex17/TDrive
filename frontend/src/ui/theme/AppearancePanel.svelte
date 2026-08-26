<script lang="ts">
    import CheckIcon from '@lucide/svelte/icons/check';
    import MoonIcon from '@lucide/svelte/icons/moon';
    import SunIcon from '@lucide/svelte/icons/sun';
    import { onMount, tick } from 'svelte';
    import {
        getThemeDefinition,
        themesForAppearance,
        type ThemeAppearance,
        type ThemeDefinition,
        type ThemeMode,
    } from './theme-model';
    import {
        setPreferredTheme,
        setThemeMode,
        themeState,
        type ThemeChangeOrigin,
    } from './theme-controller';

    interface Props {
        autofocus?: boolean;
    }

    interface ModeOption {
        id: ThemeMode;
        label: string;
        icon: typeof SunIcon;
    }

    let { autofocus = false }: Props = $props();
    let panel = $state<HTMLElement | null>(null);

    const modeOptions: readonly ModeOption[] = [
        { id: 'light', label: 'Light', icon: SunIcon },
        { id: 'dark', label: 'Dark', icon: MoonIcon },
    ];

    const activeAppearance = $derived<ThemeAppearance>($themeState.preference.mode);
    const visibleThemes = $derived(themesForAppearance(activeAppearance));
    const selectedThemeId = $derived(
        activeAppearance === 'light'
            ? $themeState.preference.lightThemeId
            : $themeState.preference.darkThemeId,
    );
    const resolvedThemeName = $derived(getThemeDefinition($themeState.resolvedThemeId).name);

    onMount(() => {
        if (!autofocus) return;
        void tick().then(() => {
            panel?.querySelector<HTMLElement>(`#appearance-mode-${$themeState.preference.mode}`)?.focus();
        });
    });

    function originFrom(event: MouseEvent): ThemeChangeOrigin {
        return { x: event.clientX, y: event.clientY };
    }

    function selectMode(event: MouseEvent, mode: ThemeMode): void {
        setThemeMode(mode, originFrom(event));
    }

    function selectTheme(event: MouseEvent, theme: ThemeDefinition): void {
        setPreferredTheme(theme.appearance, theme.id, originFrom(event));
    }

    async function moveModeFocus(event: KeyboardEvent, index: number): Promise<void> {
        const nextIndex = nextRadioIndex(event, index, modeOptions.length);
        if (nextIndex === null) return;

        event.preventDefault();
        const nextMode = modeOptions[nextIndex].id;
        setThemeMode(nextMode);
        await tick();
        document.getElementById(`appearance-mode-${nextMode}`)?.focus();
    }

    async function moveThemeFocus(event: KeyboardEvent, index: number): Promise<void> {
        const nextIndex = nextRadioIndex(event, index, visibleThemes.length);
        if (nextIndex === null) return;

        event.preventDefault();
        const nextTheme = visibleThemes[nextIndex];
        setPreferredTheme(nextTheme.appearance, nextTheme.id);
        await tick();
        document.getElementById(`appearance-theme-${nextTheme.id}`)?.focus();
    }

    function nextRadioIndex(event: KeyboardEvent, index: number, length: number): number | null {
        if (event.key === 'Home') return 0;
        if (event.key === 'End') return length - 1;
        if (event.key === 'ArrowRight' || event.key === 'ArrowDown') return (index + 1) % length;
        if (event.key === 'ArrowLeft' || event.key === 'ArrowUp') return (index - 1 + length) % length;
        return null;
    }

    function previewStyle(theme: ThemeDefinition): string {
        const [canvas, surface, accent, text] = theme.preview;
        return [
            `--preview-canvas:${canvas}`,
            `--preview-surface:${surface}`,
            `--preview-accent:${accent}`,
            `--preview-text:${text}`,
        ].join(';');
    }
</script>

<section bind:this={panel} class="appearance-panel" aria-labelledby="appearance-title">
    <header class="appearance-header">
        <h2 id="appearance-title">Appearance</h2>
    </header>

    <div class="appearance-section mode-section">
        <div class="mode-grid" role="radiogroup" aria-label="Appearance mode">
            {#each modeOptions as option, index (option.id)}
                {@const Icon = option.icon}
                {@const selected = $themeState.preference.mode === option.id}
                <button
                    id={`appearance-mode-${option.id}`}
                    data-appearance-mode={option.id}
                    data-theme-hit-target
                    class:selected
                    class="mode-card"
                    type="button"
                    role="radio"
                    aria-checked={selected}
                    tabindex={selected ? 0 : -1}
                    onclick={(event) => selectMode(event, option.id)}
                    onkeydown={(event) => void moveModeFocus(event, index)}
                >
                    <span class="mode-icon"><Icon size={17} strokeWidth={2} aria-hidden="true" /></span>
                    <span>{option.label}</span>
                </button>
            {/each}
        </div>
    </div>

    <div id="appearance-palette-panel" class="appearance-section palette-section">
        <div class="section-heading">
            <span>{activeAppearance === 'light' ? 'Light palettes' : 'Dark palettes'}</span>
            <span class="theme-count">{visibleThemes.length}</span>
        </div>
        <div class="theme-grid" role="radiogroup" aria-label="Theme palette">
            {#each visibleThemes as theme, index (theme.id)}
                {@const selected = selectedThemeId === theme.id}
                <button
                    id={`appearance-theme-${theme.id}`}
                    data-theme-hit-target
                    class:selected
                    class="theme-card"
                    type="button"
                    role="radio"
                    aria-checked={selected}
                    aria-label={theme.name}
                    tabindex={selected ? 0 : -1}
                    style={previewStyle(theme)}
                    onclick={(event) => selectTheme(event, theme)}
                    onkeydown={(event) => void moveThemeFocus(event, index)}
                >
                    <span class="theme-preview" aria-hidden="true">
                        <span class="preview-sidebar">
                            <span></span><span></span><span></span>
                        </span>
                        <span class="preview-content">
                            <span class="preview-topline"></span>
                            <span class="preview-row"><i></i><b></b></span>
                            <span class="preview-row short"><i></i><b></b></span>
                        </span>
                    </span>
                    <span class="theme-label">
                        <span class="theme-name">{theme.name}</span>
                    </span>
                    <span class="theme-check" aria-hidden="true">
                        {#if selected}<CheckIcon size={13} strokeWidth={3} />{/if}
                    </span>
                </button>
            {/each}
        </div>
    </div>

    <p class="appearance-status" aria-live="polite">{resolvedThemeName} is active.</p>
</section>

<style>
    .appearance-panel {
        width: min(390px, calc(100vw - 32px));
        max-height: min(680px, calc(100vh - 92px));
        overflow: auto;
        scrollbar-width: none;
        color: var(--color-text);
    }

    .appearance-panel::-webkit-scrollbar { display: none; }

    .appearance-header {
        display: flex;
        align-items: center;
        justify-content: flex-start;
        height: auto;
        min-height: 54px;
        padding: 8px 8px 16px;
        border-bottom: 1px solid var(--color-border-soft);
    }

    .appearance-header h2 {
        margin: 0;
        color: var(--color-text);
        font-size: 1.05rem;
        font-weight: 800;
        letter-spacing: 0;
    }

    .appearance-section { padding: 0 8px 14px; }
    .mode-section { padding: 14px 8px 19px; }

    .section-heading {
        display: flex;
        align-items: center;
        justify-content: space-between;
        min-height: 20px;
        margin-bottom: 8px;
        color: var(--color-text-muted);
        font-size: 0.69rem;
        font-weight: 800;
        letter-spacing: 0.07em;
        text-transform: uppercase;
    }

    .mode-grid {
        display: grid;
        grid-template-columns: repeat(2, minmax(0, 1fr));
        gap: 7px;
    }

    .mode-card {
        display: flex;
        flex-direction: column;
        align-items: center;
        justify-content: center;
        gap: 6px;
        min-height: 68px;
        padding: 8px;
        color: var(--color-text-muted);
        background: var(--overlay-white-1);
        border: 1px solid var(--color-border-soft);
        border-radius: 12px;
        font-size: 0.75rem;
        font-weight: 750;
        cursor: pointer;
        transition: transform var(--motion-med) var(--ease-standard),
            background var(--motion-med) var(--ease-standard),
            border-color var(--motion-med) var(--ease-standard),
            color var(--motion-med) var(--ease-standard),
            box-shadow var(--motion-med) var(--ease-standard);
    }

    .mode-card:hover { transform: translateY(-1px); color: var(--color-text); }
    .mode-card:active { transform: translateY(0) scale(0.98); }
    .mode-card:focus-visible { outline: none; box-shadow: var(--focus-ring); }
    .mode-card.selected {
        color: var(--color-accent);
        background: var(--overlay-accent-1);
        border-color: var(--overlay-accent-3);
        box-shadow: 0 7px 20px color-mix(in srgb, var(--color-accent) 9%, transparent);
    }

    .mode-icon {
        display: inline-flex;
        align-items: center;
        justify-content: center;
        width: 26px;
        height: 26px;
        border-radius: 8px;
        background: var(--overlay-white-1);
    }

    .mode-card.selected .mode-icon { background: var(--overlay-accent-2); }

    .palette-section { padding-bottom: 9px; }
    .theme-count {
        display: inline-flex;
        align-items: center;
        justify-content: center;
        min-width: 20px;
        height: 18px;
        padding: 0 5px;
        color: var(--color-text-subtle);
        background: var(--overlay-white-1);
        border-radius: 999px;
        font-size: 0.68rem;
    }

    .theme-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px; }

    .theme-card {
        position: relative;
        display: flex;
        flex-direction: column;
        gap: 8px;
        min-width: 0;
        padding: 7px;
        overflow: hidden;
        color: var(--color-text);
        text-align: left;
        background: var(--overlay-white-1);
        border: 1px solid var(--color-border-soft);
        border-radius: 13px;
        cursor: pointer;
        transition: transform var(--motion-med) var(--ease-standard),
            border-color var(--motion-med) var(--ease-standard),
            box-shadow var(--motion-med) var(--ease-standard),
            background var(--motion-med) var(--ease-standard);
    }

    .theme-card:hover { transform: translateY(-2px); border-color: var(--color-border); }
    .theme-card:active { transform: translateY(0) scale(0.985); }
    .theme-card:focus-visible { outline: none; box-shadow: var(--focus-ring); }
    .theme-card.selected {
        background: var(--overlay-accent-1);
        border-color: var(--color-accent);
        box-shadow: 0 9px 24px color-mix(in srgb, var(--color-accent) 12%, transparent);
    }

    .theme-preview {
        display: grid;
        flex: 0 0 62px;
        grid-template-columns: 32px 1fr;
        width: 100%;
        height: 62px;
        overflow: hidden;
        background: var(--preview-canvas);
        border: 1px solid color-mix(in srgb, var(--preview-text) 13%, transparent);
        border-radius: 9px;
        box-shadow: var(--shadow-sm);
    }

    .preview-sidebar {
        display: flex;
        flex-direction: column;
        gap: 5px;
        padding: 9px 6px;
        background: var(--preview-surface);
    }

    .preview-sidebar span { width: 100%; height: 3px; background: color-mix(in srgb, var(--preview-text) 22%, transparent); border-radius: 3px; }
    .preview-sidebar span:first-child { background: var(--preview-accent); }
    .preview-sidebar span:last-child { width: 70%; }

    .preview-content { display: flex; flex-direction: column; gap: 7px; padding: 8px; }
    .preview-topline { width: 72%; height: 4px; background: color-mix(in srgb, var(--preview-text) 62%, transparent); border-radius: 4px; }
    .preview-row { display: flex; align-items: center; gap: 5px; }
    .preview-row i { width: 7px; height: 7px; background: var(--preview-accent); border-radius: 2px; }
    .preview-row b { width: 64%; height: 3px; background: color-mix(in srgb, var(--preview-text) 28%, transparent); border-radius: 4px; }
    .preview-row.short b { width: 42%; }

    .theme-label {
        display: block;
        min-width: 0;
        padding: 0 3px 2px;
    }
    .theme-name {
        display: block;
        overflow: hidden;
        font-size: 0.75rem;
        font-weight: 800;
        line-height: 1.25;
        text-overflow: ellipsis;
        white-space: nowrap;
    }

    .theme-check {
        position: absolute;
        top: 12px;
        right: 12px;
        display: inline-flex;
        align-items: center;
        justify-content: center;
        width: 20px;
        height: 20px;
        color: var(--color-on-accent);
        background: var(--color-accent);
        border: 2px solid var(--preview-canvas);
        border-radius: 999px;
        opacity: 0;
        transform: scale(0.75);
        transition: opacity var(--motion-med) var(--ease-standard), transform var(--motion-med) var(--ease-standard);
    }

    .theme-card.selected .theme-check { opacity: 1; transform: scale(1); }

    .appearance-status {
        position: absolute;
        width: 1px;
        height: 1px;
        padding: 0;
        margin: -1px;
        overflow: hidden;
        clip: rect(0, 0, 0, 0);
        white-space: nowrap;
        border: 0;
    }

    @media (max-width: 460px) {
        .appearance-panel { width: min(350px, calc(100vw - 24px)); }
        .theme-grid { grid-template-columns: 1fr; }
    }

    @media (prefers-reduced-motion: reduce) {
        .mode-card,
        .theme-card,
        .theme-check { transition: none; }

        .mode-card:hover,
        .theme-card:hover { transform: none; }
    }
</style>
