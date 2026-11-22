<script lang="ts">
    import { isApplePlatform } from '../../utils';
    import ArrowLeftIcon from '@lucide/svelte/icons/arrow-left';
    import ArrowRightIcon from '@lucide/svelte/icons/arrow-right';
    import ArrowUpIcon from '@lucide/svelte/icons/arrow-up';
    import { breadcrumbPath, type BreadcrumbDrag } from './breadcrumb-store';
    import { sidebarState } from '../sidebar/sidebar-store';

    interface Props {
        // index -1 targets the drive root, otherwise a folderPath index.
        onNavigate: (index: number) => void;
        onBack: () => void;
        drag: BreadcrumbDrag;
        // Back (-1) and forward (+1) through visited folders.
        onHistory?: (step: -1 | 1) => void;
        canGoBack?: boolean;
        canGoForward?: boolean;
    }

    let { onNavigate, onBack, drag, onHistory = () => undefined, canGoBack = false, canGoForward = false }: Props = $props();
    const apple = isApplePlatform();

    interface CrumbItem {
        id: string;
        name: string;
        index: number;
    }

    // The root is the active drive, named: a shared drive is not "My Drive".
    const rootName = $derived.by(() => {
        const { personal, shared, activeChannelId } = $sidebarState;
        const active = [...personal, ...shared].find((drive) => drive.id === activeChannelId);
        return active?.title?.trim() || 'My Drive';
    });

    const items = $derived<CrumbItem[]>([
        { id: '', name: rootName, index: -1 },
        ...$breadcrumbPath.map((f, i) => ({ id: f.id, name: f.name, index: i })),
    ]);
    const atRoot = $derived($breadcrumbPath.length === 0);

    function registerRoot(el: HTMLElement, isRoot: boolean) {
        if (isRoot) drag.registerRoot(el);
    }

    function onDragOver(event: DragEvent, item: CrumbItem): void {
        if (!drag.isActive()) return;
        const el = event.currentTarget as HTMLElement;
        const allowed = drag.canDrop(item.id);
        drag.highlight(el, allowed);
        if (event.dataTransfer) event.dataTransfer.dropEffect = allowed ? 'move' : 'none';
        if (allowed) event.preventDefault();
    }

    function onDragLeave(event: DragEvent): void {
        const el = event.currentTarget as HTMLElement;
        if (event.relatedTarget && el.contains(event.relatedTarget as Node)) return;
        drag.leave(el);
    }

    function onDrop(event: DragEvent, item: CrumbItem): void {
        if (!drag.isActive() || !drag.canDrop(item.id)) return;
        event.preventDefault();
        event.stopPropagation();
        drag.dropOn(event.currentTarget as HTMLElement, item.id);
    }
</script>

<!-- Back and forward appear once there is a visited folder to return to,
     then stay put, disabled at either end, so neither jumps under the pointer. -->
{#if canGoBack || canGoForward}
    <button
        id="breadcrumb-history-back"
        class="icon-btn back-btn history-btn"
        type="button"
        title={apple ? "Back (⌘[)" : "Back (Alt+←)"}
        aria-label="Back"
        aria-keyshortcuts={apple ? "Meta+BracketLeft" : "Alt+ArrowLeft"}
        disabled={!canGoBack}
        onclick={() => onHistory(-1)}
    >
        <ArrowLeftIcon size={18} strokeWidth={2} aria-hidden="true" />
    </button>
    <button
        id="breadcrumb-history-forward"
        class="icon-btn back-btn history-btn"
        type="button"
        title={apple ? "Forward (⌘])" : "Forward (Alt+→)"}
        aria-label="Forward"
        aria-keyshortcuts={apple ? "Meta+BracketRight" : "Alt+ArrowRight"}
        disabled={!canGoForward}
        onclick={() => onHistory(1)}
    >
        <ArrowRightIcon size={18} strokeWidth={2} aria-hidden="true" />
    </button>
{/if}
<!-- Absent at the root, where the drive name is the whole title. -->
<button
    id="breadcrumb-back"
    class="icon-btn back-btn"
    class:is-hidden={atRoot}
    type="button"
    title={apple ? "Parent folder (⌘↑ or Alt+↑)" : "Parent folder (Alt+↑ or Backspace)"}
    aria-label="Parent folder"
    aria-keyshortcuts={apple ? "Meta+ArrowUp Alt+ArrowUp" : "Alt+ArrowUp Backspace"}
    disabled={atRoot}
    onclick={onBack}
>
    <ArrowUpIcon size={18} strokeWidth={2} aria-hidden="true" />
</button>
<nav id="breadcrumb-path" class="breadcrumb-path" aria-label="Folder path">
    {#each items as item, idx (item.index === -1 ? 'root' : item.id)}
        {#if idx > 0}
            <span class="breadcrumb-sep" aria-hidden="true">/</span>
        {/if}
        <button
            type="button"
            class="breadcrumb-link"
            data-index={String(item.index)}
            aria-current={idx === items.length - 1 ? 'location' : undefined}
            use:registerRoot={item.index === -1}
            onclick={() => onNavigate(item.index)}
            ondragover={(event) => onDragOver(event, item)}
            ondragleave={onDragLeave}
            ondrop={(event) => onDrop(event, item)}
        >
            {item.name}
        </button>
    {/each}
</nav>

<style>
    /* At the root there is nowhere to go back to, and the title starts flush. */
    .is-hidden { display: none; }
</style>
