<script lang="ts">
    import { tick } from 'svelte';
    import { contextMenuState, hideContextMenu, type ContextMenuItem } from './context-menu-store';

    const VIEWPORT_MARGIN = 8;

    let panel = $state<HTMLElement | null>(null);
    let left = $state(0);
    let top = $state(0);
    let lastFocusVersion = 0;

    function menuButtons(): HTMLButtonElement[] {
        if (!panel) return [];
        return Array.from(panel.querySelectorAll<HTMLButtonElement>('button[role="menuitem"]:not(:disabled)'));
    }

    function focusMenuItem(delta: number): void {
        const items = menuButtons();
        if (!items.length) return;
        const current = document.activeElement instanceof HTMLButtonElement
            ? items.indexOf(document.activeElement)
            : -1;
        const next = current < 0 ? 0 : (current + delta + items.length) % items.length;
        items[next]?.focus();
    }

    function focusMenuEdge(edge: 'first' | 'last'): void {
        const items = menuButtons();
        const target = edge === 'first' ? items[0] : items[items.length - 1];
        target?.focus();
    }

    async function positionHost(): Promise<void> {
        const state = $contextMenuState;
        if (!state.open) return;

        left = state.x;
        top = state.y;
        await tick();

        if (!panel) return;
        const rect = panel.getBoundingClientRect();
        const maxX = Math.max(VIEWPORT_MARGIN, window.innerWidth - rect.width - VIEWPORT_MARGIN);
        const maxY = Math.max(VIEWPORT_MARGIN, window.innerHeight - rect.height - VIEWPORT_MARGIN);
        left = Math.max(VIEWPORT_MARGIN, Math.min(state.x, maxX));
        top = Math.max(VIEWPORT_MARGIN, Math.min(state.y, maxY));

        if (lastFocusVersion !== state.focusVersion) {
            lastFocusVersion = state.focusVersion;
            requestAnimationFrame(() => focusMenuEdge('first'));
        }
    }

    function invoke(item: ContextMenuItem): void {
        if (item.type === 'divider' || item.disabled) return;
        hideContextMenu();
        void item.action();
    }

    function onDocumentClick(event: MouseEvent): void {
        if (!$contextMenuState.open) return;
        if (panel?.contains(event.target as Node)) return;
        hideContextMenu();
    }

    function onDocumentKeydown(event: KeyboardEvent): void {
        if (!$contextMenuState.open) return;
        if (event.key === 'Escape') {
            event.preventDefault();
            hideContextMenu();
            return;
        }
        if (event.key === 'ArrowDown') {
            event.preventDefault();
            focusMenuItem(1);
            return;
        }
        if (event.key === 'ArrowUp') {
            event.preventDefault();
            focusMenuItem(-1);
            return;
        }
        if (event.key === 'Home') {
            event.preventDefault();
            focusMenuEdge('first');
            return;
        }
        if (event.key === 'End') {
            event.preventDefault();
            focusMenuEdge('last');
        }
    }

    $effect(() => {
        void positionHost();
    });
</script>

<svelte:document onclick={onDocumentClick} onkeydown={onDocumentKeydown} />

{#if $contextMenuState.open}
    <div
        bind:this={panel}
        class="context-menu-panel"
        role="menu"
        style:left={`${left}px`}
        style:top={`${top}px`}
    >
        {#each $contextMenuState.items as item, index (item.type === 'divider' ? `divider-${index}` : `${item.label}-${index}`)}
            {#if item.type === 'divider'}
                <div class="divider" role="separator"></div>
            {:else}
                <button
                    type="button"
                    role="menuitem"
                    class:danger={item.danger}
                    disabled={item.disabled}
                    tabindex="-1"
                    onclick={() => invoke(item)}
                >
                    {item.label}
                </button>
            {/if}
        {/each}
    </div>
{/if}
