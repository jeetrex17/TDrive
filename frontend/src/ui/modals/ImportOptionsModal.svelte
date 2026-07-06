<script lang="ts">
    import ModalShell from './ModalShell.svelte';
    import { importOptionsModal } from './import-options-modal-store';

    interface Props {
        onCancel: () => void;
        onConfirm: (choice: { encrypt: boolean; extract: boolean }) => void;
        onToggle: (encrypt: boolean, extract: boolean) => void;
    }

    let { onCancel, onConfirm, onToggle }: Props = $props();
    let extract = $state(false);
    let encrypt = $state(false);
    let wasOpen = false;

    const view = importOptionsModal.state;

    function humanBytes(n: number): string {
        if (!Number.isFinite(n) || n <= 0) return '0 B';
        const units = ['B', 'KB', 'MB', 'GB', 'TB'];
        let value = n;
        let i = 0;
        while (value >= 1024 && i < units.length - 1) {
            value /= 1024;
            i++;
        }
        const rounded = value < 10 && i > 0 ? value.toFixed(1) : String(Math.round(value));
        return `${rounded} ${units[i]}`;
    }

    function plural(n: number, one: string, many: string): string {
        return `${n} ${n === 1 ? one : many}`;
    }

    const summary = $derived.by(() => {
        const plan = $view.payload?.plan;
        if (!plan) return '';
        const parts = [plural(plan.files, 'file', 'files')];
        if (plan.folders > 0) parts.push(plural(plan.folders, 'folder', 'folders'));
        if (plan.archives > 0) parts.push(plural(plan.archives, 'archive', 'archives'));
        let text = parts.join('  ·  ');
        if (plan.bytes > 0) text += `  ·  ${humanBytes(plan.bytes)}`;
        return text;
    });

    const note = $derived.by(() => {
        const plan = $view.payload?.plan;
        if (!plan) return '';
        const errors = Array.isArray(plan.errors) ? plan.errors.length : 0;
        const notes: string[] = [];
        if (plan.oversize > 0) {
            notes.push(`${plural(plan.oversize, 'file', 'files')} over ${humanBytes(plan.maxBytes)} will be skipped (Telegram's per-file limit).`);
        }
        if (errors > 0) {
            notes.push(`${plural(errors, 'item', 'items')} could not be scanned and may be skipped or uploaded as-is.`);
        }
        return notes.join(' ');
    });

    function toggled(): void {
        onToggle(encrypt, extract);
    }

    $effect(() => {
        if ($view.open && !wasOpen) {
            extract = false;
            encrypt = false;
        }
        wasOpen = $view.open;
    });
</script>

<ModalShell
    hostId="import-options-modal"
    open={$view.open}
    title="Import"
    titleId="import-options-title"
    subtitle={summary}
    initialFocus="#import-options-extract"
    restoreFocus="#file-list"
    onClose={onCancel}
>
    {#if $view.payload?.hasArchives}
        <label id="import-options-extract-row" class="modal-choice-row">
            <input id="import-options-extract" type="checkbox" bind:checked={extract} onchange={toggled} />
            <span>
                <strong>Extract archives</strong>
                <small>Unpack archive contents into folders instead of uploading the archive file.</small>
            </span>
        </label>
    {/if}
    {#if $view.payload?.personal}
        <label id="import-options-encrypt-row" class="modal-choice-row">
            <input id="import-options-encrypt" type="checkbox" bind:checked={encrypt} onchange={toggled} />
            <span>
                <strong>Encrypt before upload</strong>
                <small>TDrive encrypts file contents before sending them to Telegram. Names and folders stay visible.</small>
            </span>
        </label>
    {/if}
    {#if note}
        <p id="import-options-note" class="import-options-note">{note}</p>
    {/if}

    {#snippet actions()}
        <button id="import-options-cancel" class="secondary-btn" type="button" onclick={onCancel}>Cancel</button>
        <button
            id="import-options-confirm"
            class="primary-btn"
            type="button"
            onclick={() => onConfirm({ encrypt, extract })}
        >
            Import
        </button>
    {/snippet}
</ModalShell>
