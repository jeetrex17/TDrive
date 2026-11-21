<script lang="ts">
    import { isApplePlatform } from '../../utils';
    import ChevronLeftIcon from '@lucide/svelte/icons/chevron-left';
    import { breadcrumbPath, type BreadcrumbDrag } from './breadcrumb-store';
    import { sidebarState } from '../sidebar/sidebar-store';

    interface Props {
        // index -1 targets the drive root, otherwise a folderPath index.
        onNavigate: (index: number) => void;
        onBack: () => void;
        drag: BreadcrumbDrag;
    }

    let { onNavigate, onBack, drag }: Props = $props();

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

<!-- Absent at the root, where the drive name is the whole title. -->
<button
    id="breadcrumb-back"
    class="icon-btn back-btn"
    class:is-hidden={atRoot}
    type="button"
    title={isApplePlatform() ? "Parent folder (⌘↑ or Alt+↑)" : "Parent folder (Alt+↑)"}
    aria-label="Parent folder"
    aria-keyshortcuts={isApplePlatform() ? "Meta+ArrowUp Alt+ArrowUp" : "Alt+ArrowUp"}
    disabled={atRoot}
    onclick={onBack}
>
    <ChevronLeftIcon size={18} strokeWidth={2} aria-hidden="true" />
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
