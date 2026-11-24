<script lang="ts">
    import { tick, untrack } from 'svelte';
    import { get } from 'svelte/store';
    import { isApplePlatform } from '../../utils';
    import ArrowLeftIcon from '@lucide/svelte/icons/arrow-left';
    import ArrowRightIcon from '@lucide/svelte/icons/arrow-right';
    import ArrowUpIcon from '@lucide/svelte/icons/arrow-up';
    import PencilIcon from '@lucide/svelte/icons/pencil';
    import { breadcrumbEditRequest, breadcrumbPath, type BreadcrumbDrag } from './breadcrumb-store';
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
        // Opens a typed path; resolves to a message to show, or null once it
        // has gone there. Without it the trail cannot be edited.
        onPathSubmit?: (text: string, stillWanted: () => boolean) => Promise<string | null>;
    }

    let {
        onNavigate,
        onBack,
        drag,
        onHistory = () => undefined,
        canGoBack = false,
        canGoForward = false,
        onPathSubmit,
    }: Props = $props();
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

    // The path field. It stands in for the trail, as File Explorer's address
    // bar does, and shows the folder as a path from the drive root.
    let editing = $state(false);
    let draft = $state('');
    let error = $state('');
    let busy = $state(false);
    let editButton = $state<HTMLButtonElement | null>(null);
    let returnFocus: HTMLElement | null = null;
    let editingDrive: number | null = null;
    // Each submit or cancel takes a new number, so a late answer for an
    // abandoned path is dropped.
    let attempt = 0;
    let lastEditRequest = get(breadcrumbEditRequest);

    function startEditing(): void {
        const { activeChannelId, virtualView } = get(sidebarState);
        if (!onPathSubmit || editing || virtualView !== null) return;
        const active = document.activeElement;
        returnFocus = active instanceof HTMLElement && active !== document.body ? active : null;
        editingDrive = activeChannelId;
        draft = '/' + $breadcrumbPath.map((entry) => entry.name).join('/');
        error = '';
        busy = false;
        editing = true;
    }

    function stopEditing(restoreFocus: boolean): void {
        if (!editing) return;
        attempt += 1;
        editing = false;
        busy = false;
        error = '';
        if (!restoreFocus) return;
        const target = returnFocus?.isConnected ? returnFocus : null;
        returnFocus = null;
        void tick().then(() => (target ?? editButton)?.focus());
    }

    async function submit(): Promise<void> {
        if (!onPathSubmit || busy) return;
        const mine = ++attempt;
        busy = true;
        error = '';
        let message: string | null;
        try {
            message = await onPathSubmit(draft, () => mine === attempt);
        } catch {
            message = 'Could not read the drive. Try again.';
        }
        if (mine !== attempt) return;
        busy = false;
        if (message) {
            error = message;
            return;
        }
        returnFocus = null;
        editing = false;
    }

    function onKeydown(event: KeyboardEvent): void {
        // Enter also confirms an IME composition; that one is not a submit.
        if (event.isComposing || event.keyCode === 229) return;
        if (event.key === 'Escape') {
            event.preventDefault();
            event.stopPropagation();
            stopEditing(true);
        } else if (event.key === 'Enter') {
            event.preventDefault();
            void submit();
        }
    }

    function onBlur(): void {
        // Switching to another window to copy a path keeps the field open.
        if (!document.hasFocus()) return;
        stopEditing(false);
    }

    function onPaste(event: ClipboardEvent): void {
        const text = event.clipboardData?.getData('text/plain') ?? '';
        if (!/[\r\n]/.test(text)) return;
        // A text field joins pasted lines into one, which no path survives;
        // keep the first line that has anything on it.
        event.preventDefault();
        const line = text.split(/\r\n|\r|\n/).map((part) => part.trim()).find((part) => part !== '') ?? '';
        const input = event.currentTarget as HTMLInputElement;
        input.setRangeText(line, input.selectionStart ?? input.value.length, input.selectionEnd ?? input.value.length, 'end');
        draft = input.value;
        error = '';
    }

    function selectOnMount(input: HTMLInputElement): void {
        input.focus();
        input.select();
    }

    // A click beside the crumbs, or on a separator, opens the field. Keyboard
    // users have Ctrl/Cmd+L and the Edit path button for the same thing.
    function editOnEmptyClick(nav: HTMLElement) {
        const onClick = (event: MouseEvent) => {
            const target = event.target as Element | null;
            if (target === nav || target?.classList.contains('breadcrumb-sep')) startEditing();
        };
        nav.addEventListener('click', onClick);
        return { destroy: () => nav.removeEventListener('click', onClick) };
    }

    $effect(() => {
        const request = $breadcrumbEditRequest;
        if (request === lastEditRequest) return;
        lastEditRequest = request;
        untrack(() => {
            if (editing) {
                const input = document.getElementById('breadcrumb-path-input');
                if (input instanceof HTMLInputElement) input.select();
                return;
            }
            startEditing();
        });
    });

    // Media, Trash or another drive take the place the path described.
    $effect(() => {
        const { activeChannelId, virtualView } = $sidebarState;
        untrack(() => {
            if (editing && (virtualView !== null || activeChannelId !== editingDrive)) stopEditing(false);
        });
    });

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
{#if editing}
    <div class="breadcrumb-path-editor">
        <input
            id="breadcrumb-path-input"
            type="text"
            bind:value={draft}
            aria-label="Folder path"
            aria-invalid={error ? 'true' : undefined}
            aria-describedby={error ? 'breadcrumb-path-error' : undefined}
            aria-busy={busy ? 'true' : undefined}
            readonly={busy}
            autocomplete="off"
            spellcheck="false"
            use:selectOnMount
            oninput={() => { error = ''; }}
            onkeydown={onKeydown}
            onblur={onBlur}
            onpaste={onPaste}
        />
        {#if error}
            <p id="breadcrumb-path-error" class="breadcrumb-path-error" role="alert">{error}</p>
        {/if}
    </div>
{:else}
    <nav
        id="breadcrumb-path"
        class="breadcrumb-path"
        class:is-editable={Boolean(onPathSubmit)}
        aria-label="Folder path"
        use:editOnEmptyClick
    >
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
    {#if onPathSubmit}
        <!-- Takes no room until it has keyboard focus, so the trail keeps its
             place; a pointer opens the field from the space beside the crumbs. -->
        <button
            id="breadcrumb-edit"
            class="icon-btn back-btn breadcrumb-edit-btn"
            type="button"
            title={apple ? "Edit path (⌘L)" : "Edit path (Ctrl+L)"}
            aria-label="Edit path"
            aria-keyshortcuts={apple ? "Meta+L" : "Control+L"}
            bind:this={editButton}
            onclick={startEditing}
        >
            <PencilIcon size={16} strokeWidth={2} aria-hidden="true" />
        </button>
    {/if}
{/if}

<style>
    /* At the root there is nowhere to go back to, and the title starts flush. */
    .is-hidden { display: none; }

    /* The trail fills the row so the space beside the crumbs can open the
       path field; the crumbs themselves still navigate. */
    .breadcrumb-path.is-editable { flex: 1 1 auto; cursor: text; }

    .breadcrumb-edit-btn { flex: 0 0 auto; }
    .breadcrumb-edit-btn:not(:focus-visible) {
        width: 0;
        min-width: 0;
        padding: 0;
        /* Cancels the row's gap, so an unfocused button moves nothing. */
        margin-inline-start: calc(-1 * var(--space-1-5));
        overflow: hidden;
        opacity: 0;
    }
    :global(.main-content.trash-mode) .breadcrumb-edit-btn,
    :global(.main-content.photos-mode) .breadcrumb-edit-btn { display: none; }

    .breadcrumb-path-editor {
        flex: 1 1 0;
        min-width: 0;
        position: relative;
    }

    /* Matches the search field beside it, without the room for its icon. */
    .breadcrumb-path-editor input {
        display: block;
        width: 100%;
        min-width: 0;
        height: var(--toolbar-control);
        min-height: 0;
        margin: 0;
        padding: 0 10px;
        background: var(--color-surface-1);
        border: 1px solid var(--color-border-soft);
        border-radius: var(--radius-md);
        font-size: var(--type-sm);
    }
    .breadcrumb-path-editor input:focus {
        border-color: var(--accent);
        box-shadow: var(--focus-ring);
    }
    .breadcrumb-path-editor input[aria-invalid="true"] { border-color: var(--color-danger); }
    .breadcrumb-path-editor input[aria-busy="true"] { cursor: progress; }

    /* Hangs below the field instead of growing the top bar. */
    .breadcrumb-path-error {
        position: absolute;
        top: calc(100% + 6px);
        inset-inline-start: 0;
        z-index: var(--z-popover);
        width: max-content;
        max-width: 100%;
        margin: 0;
        padding: var(--space-2) var(--space-3);
        overflow-wrap: anywhere;
        background: var(--color-surface-2);
        border: 1px solid var(--glass-border);
        border-radius: var(--radius-md);
        box-shadow: var(--shadow-md);
        color: var(--color-danger);
        font-size: var(--type-sm);
    }
</style>
