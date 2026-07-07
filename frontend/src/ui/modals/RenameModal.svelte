<script lang="ts">
    import { tick } from 'svelte';
    import ModalShell from './ModalShell.svelte';
    import {
        closeRenameModalView,
        renameModalState,
        type RenameModalTarget,
    } from './rename-modal-store';

    interface Props {
        onSubmit: (target: RenameModalTarget, nextName: string) => void | Promise<void>;
    }

    let { onSubmit }: Props = $props();
    let name = $state('');
    let inputEl = $state<HTMLInputElement | null>(null);
    let wasOpen = false;

    const isFolder = $derived($renameModalState.target?.type === 'folder');

    function close(): void {
        if ($renameModalState.inFlight) return;
        closeRenameModalView();
    }

    function submit(): void {
        const target = $renameModalState.target;
        if (!target || $renameModalState.inFlight) return;
        void onSubmit(target, name);
    }

    function onInputKeydown(event: KeyboardEvent): void {
        if (event.key === 'Enter') {
            event.preventDefault();
            submit();
        }
        if (event.key === 'Escape') {
            event.preventDefault();
            close();
        }
    }

    // Pre-select the base name (before the extension) for files, the whole
    // name for folders after Svelte has applied the opened state.
    function selectNameRange(): void {
        const el = inputEl;
        if (!el || !el.isConnected) return;
        const value = el.value || '';
        const dot = value.lastIndexOf('.');
        if (!isFolder && dot > 0 && dot < value.length - 1) {
            el.setSelectionRange(0, dot);
        } else {
            el.select();
        }
    }

    $effect(() => {
        if ($renameModalState.open && !wasOpen) {
            name = $renameModalState.target?.name ?? '';
            void tick().then(selectNameRange);
        }
        wasOpen = $renameModalState.open;
    });
</script>

<ModalShell
    hostId="rename-modal"
    open={$renameModalState.open}
    title={isFolder ? 'Rename folder' : 'Rename file'}
    titleId="rename-modal-title"
    subtitle={isFolder ? 'Choose a new folder name.' : 'Choose a new file name.'}
    initialFocus="#rename-input"
    restoreFocus="#file-list"
    onClose={close}
>
    <input
        id="rename-input"
        type="text"
        placeholder="Name"
        autocomplete="off"
        bind:this={inputEl}
        bind:value={name}
        disabled={$renameModalState.inFlight}
        onkeydown={onInputKeydown}
    />
    {#if $renameModalState.error}
        <div id="rename-error" class="modal-error">{$renameModalState.error}</div>
    {/if}

    {#snippet actions()}
        <button
            id="rename-cancel"
            class="secondary-btn"
            type="button"
            disabled={$renameModalState.inFlight}
            onclick={close}
        >
            Cancel
        </button>
        <button
            id="rename-confirm"
            class="primary-btn"
            type="button"
            disabled={$renameModalState.inFlight || !name.trim()}
            onclick={submit}
        >
            Rename
        </button>
    {/snippet}
</ModalShell>
