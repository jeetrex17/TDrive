<script lang="ts">
    import { tick, untrack } from 'svelte';
    import { get } from 'svelte/store';
    import { isApplePlatform } from '../../utils';
    import ArrowLeftIcon from '@lucide/svelte/icons/arrow-left';
    import ArrowRightIcon from '@lucide/svelte/icons/arrow-right';
    import ArrowUpIcon from '@lucide/svelte/icons/arrow-up';
    import TextCursorInputIcon from '@lucide/svelte/icons/text-cursor-input';
    import { firstLine, formatDrivePath } from '../../modules/drive-path';
    import { breadcrumbEditRequest, breadcrumbPath, type BreadcrumbDrag } from './breadcrumb-store';
    import { openChannelKey } from '../channels/channel-store';
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
        // Called as the field opens, so nothing queued earlier takes its focus.
        onPathEditStart?: () => void;
    }

    let {
        onNavigate,
        onBack,
        drag,
        onHistory = () => undefined,
        canGoBack = false,
        canGoForward = false,
        onPathSubmit,
        onPathEditStart = () => undefined,
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
        if (!onPathSubmit || editing || virtualView !== null || get(openChannelKey) !== null) return;
        const active = document.activeElement;
        returnFocus = active instanceof HTMLElement && active !== document.body ? active : null;
        editingDrive = activeChannelId;
        draft = formatDrivePath($breadcrumbPath.map((entry) => entry.name));
        error = '';
        busy = false;
        editing = true;
        onPathEditStart();
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
        // Reopened meanwhile: the field keeps the focus.
        void tick().then(() => {
            if (!editing) (target ?? editButton)?.focus();
        });
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
        // Ctrl/Cmd+L again selects the whole path, as it does in File Explorer.
        if (event.key.toLowerCase() === 'l' && !event.altKey && !event.shiftKey && event.ctrlKey !== event.metaKey) {
            event.preventDefault();
            (event.currentTarget as HTMLInputElement).select();
        } else if (event.key === 'Escape') {
            event.preventDefault();
            event.stopPropagation();
            stopEditing(true);
        } else if (event.key === 'Enter') {
            event.preventDefault();
            void submit();
        }
    }

    // Focus leaving the field and its message closes the field. Switching to
    // another window to copy a path keeps it open, and so does selecting the
    // message's text to copy it.
    function closeOnLeave(editor: HTMLElement) {
        const onFocusOut = (event: FocusEvent) => {
            const next = event.relatedTarget;
            if (next instanceof Node && editor.contains(next)) return;
            if (!document.hasFocus()) return;
            stopEditing(false);
        };
        // With the message focused, Escape still cancels. The folder shortcuts
        // treat the whole editor as text, so Backspace does not leave the
        // folder, while F5 still reaches the app's own refresh.
        const onMessageKeydown = (event: KeyboardEvent) => {
            if (event.target instanceof HTMLInputElement || event.key !== 'Escape') return;
            event.preventDefault();
            event.stopPropagation();
            stopEditing(true);
        };
        editor.addEventListener('focusout', onFocusOut);
        editor.addEventListener('keydown', onMessageKeydown);
        return {
            destroy: () => {
                editor.removeEventListener('focusout', onFocusOut);
                editor.removeEventListener('keydown', onMessageKeydown);
            },
        };
    }

    function onPaste(event: ClipboardEvent): void {
        if (busy) {
            event.preventDefault();
            return;
        }
        const plain = event.clipboardData?.getData('text/plain') ?? '';
        // Linux file managers copy a location as a URI list, with only the bare
        // name (Files) or nothing as plain text. The file URI says where it is,
        // so it wins, and is then told apart as a path on this computer.
        const uri = firstLine(event.clipboardData?.getData('text/uri-list') ?? '', true);
        const fromList = /^file:/i.test(uri) || (!plain && uri !== '');
        if (!fromList && !/[\r\n]/.test(plain)) return;
        // A text field joins pasted lines into one, which no path survives;
        // keep the first line that has anything on it.
        event.preventDefault();
        const line = fromList ? uri : firstLine(plain);
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
        // An open field takes Ctrl/Cmd+L itself, in onKeydown.
        untrack(startEditing);
    });

    // Media, Trash, a Telegram channel or another drive take the place the
    // path described.
    $effect(() => {
        const { activeChannelId, virtualView } = $sidebarState;
        const channel = $openChannelKey;
        untrack(() => {
            if (editing && (virtualView !== null || channel !== null || activeChannelId !== editingDrive)) stopEditing(false);
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
<!-- While the field is open the trail stays in place, unseen, and the field
     lies over it, so opening it resizes nothing else in the row. -->
<div class="breadcrumb-trail" class:is-editing={editing}>
    <nav
        id="breadcrumb-path"
        class="breadcrumb-path"
        class:is-editable={Boolean(onPathSubmit)}
        aria-label="Folder path"
        aria-hidden={editing ? 'true' : undefined}
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
        <!-- Keeps its place but stays clear until the pointer is over the path
             row or it has keyboard focus, so showing it moves no crumb. -->
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
            <TextCursorInputIcon size={16} strokeWidth={2} aria-hidden="true" />
        </button>
    {/if}
    {#if editing}
        <div class="breadcrumb-path-editor" use:closeOnLeave>
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
                onpaste={onPaste}
            />
            {#if error}
                <!-- Focusable by pointer only, so selecting its text keeps the field open. -->
                <p id="breadcrumb-path-error" class="breadcrumb-path-error" role="alert" tabindex="-1">{error}</p>
            {/if}
        </div>
    {/if}
</div>

<style>
    /* At the root there is nowhere to go back to, and the title starts flush. */
    .is-hidden { display: none; }

    .breadcrumb-trail {
        position: relative;
        display: flex;
        flex: 1 1 auto;
        align-items: center;
        gap: var(--space-1-5);
        min-width: 0;
        min-height: var(--toolbar-control);
    }
    .breadcrumb-trail.is-editing > :not(.breadcrumb-path-editor) { visibility: hidden; }

    /* The trail fills the row so the space beside the crumbs can open the
       path field; the crumbs themselves still navigate. */
    .breadcrumb-path.is-editable { flex: 1 1 auto; cursor: text; }

    .breadcrumb-edit-btn { flex: 0 0 auto; opacity: 0; }
    /* :focus, not :focus-visible, which macOS 11's WebKit does not know. */
    .breadcrumb-edit-btn:focus { opacity: 1; }
    @media (hover: hover) {
        :global(.drive-breadcrumb:hover) .breadcrumb-edit-btn { opacity: 1; }
    }
    /* A touch screen has no hover to reveal it with. */
    @media (hover: none) {
        .breadcrumb-edit-btn { opacity: 1; }
    }
    :global(.main-content.trash-mode) .breadcrumb-trail,
    :global(.main-content.photos-mode) .breadcrumb-trail { display: none; }

    .breadcrumb-path-editor {
        position: absolute;
        inset: 0;
        display: flex;
        align-items: center;
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
    /* High contrast drops the shadow ring; keep a visible focus outline. */
    @media (forced-colors: active) {
        .breadcrumb-path-editor input:focus { outline: 2px solid Highlight; outline-offset: 1px; }
    }
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
        /* Body text keeps the contrast every theme checks; the red border on
           the field already marks the error. */
        color: var(--color-text);
        font-size: var(--type-sm);
        /* The app turns text selection off; this message is meant to be copied. */
        -webkit-user-select: text;
        user-select: text;
    }
</style>
