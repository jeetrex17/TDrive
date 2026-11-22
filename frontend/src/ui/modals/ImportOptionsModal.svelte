<script lang="ts">
    import ModalShell from './ModalShell.svelte';
    import SelectedUploadFiles from './SelectedUploadFiles.svelte';
    import { importOptionsModal, type ImportChoice } from './import-options-modal-store';
    import type { ImportFileConflict } from '../../types';

    interface Props {
        onCancel: () => void;
        onConfirm: (choice: ImportChoice) => void;
        onToggle: (encrypt: boolean, extract: boolean) => void;
    }

    let { onCancel, onConfirm, onToggle }: Props = $props();
    let extract = $state(false);
    let encrypt = $state(false);
    let merge = $state(false);
    let fileConflict = $state<ImportFileConflict>('skip');
    let wasOpen = false;

    const view = importOptionsModal.state;
    const canMerge = $derived($view.payload?.allowFolderMerge === true && $view.payload.plan.folders > 0);

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
        const reportedErrors = Number(plan.errorCount);
        const errors = Number.isFinite(reportedErrors)
            ? Math.max(0, Math.floor(reportedErrors))
            : (Array.isArray(plan.errors) ? plan.errors.length : 0);
        const notes: string[] = [];
        if (plan.oversize > 0) {
            notes.push(`${plural(plan.oversize, 'file', 'files')} over ${humanBytes(plan.maxBytes)} will be skipped (Telegram's per-file limit).`);
        }
        if (plan.empty) {
            notes.push(`${plural(plan.empty, 'empty file', 'empty files')} will be skipped (empty files can't be uploaded).`);
        }
        if (plan.ignored > 0) {
            notes.push(`${plural(plan.ignored, 'generated/cache entry', 'generated/cache entries')} will be skipped.`);
        }
        if (errors > 0) {
            notes.push(`${plural(errors, 'item', 'items')} could not be scanned and may be skipped or uploaded as-is.`);
        }
        if (plan.limitExceeded) {
            notes.push(`Keep it under ${plan.maxItems.toLocaleString()} items by removing generated/cache folders or splitting it into smaller batches.`);
        }
        return notes.join(' ');
    });

    function toggled(): void {
        onToggle(encrypt, extract);
    }

    $effect(() => {
        if ($view.open && !wasOpen) {
            extract = false;
            merge = false;
            fileConflict = 'skip';
            // Starts from the remembered upload choice; the plan was made with it.
            encrypt = $view.payload?.encrypt === true;
        }
        wasOpen = $view.open;
    });
</script>

<ModalShell
    hostId="import-options-modal"
    open={$view.open}
    title={$view.payload?.queued ? 'Add import to queue' : 'Import'}
    titleId="import-options-title"
    subtitle={summary}
    initialFocus="#import-options-extract"
    restoreFocus="#file-list"
    onClose={onCancel}
>
    <SelectedUploadFiles names={$view.payload?.names ?? []} destination={$view.payload?.destination} queued={$view.payload?.queued} label="Selected items" />
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
    {#if canMerge}
        <label class="modal-choice-row">
            <input id="import-options-merge" type="checkbox" bind:checked={merge} />
            <span>
                <strong>Merge matching folders</strong>
                <small>Add to existing folders with the same name. Otherwise, import as separate folders.</small>
            </span>
        </label>
        {#if merge}
            <div role="radiogroup" aria-label="Files with the same name">
                <label class="modal-choice-row">
                    <input type="radio" name="import-file-conflict" value="skip" bind:group={fileConflict} />
                    <span>Skip files with the same name</span>
                </label>
                <label class="modal-choice-row">
                    <input type="radio" name="import-file-conflict" value="keep-both" bind:group={fileConflict} />
                    <span>Keep both files</span>
                </label>
            </div>
        {/if}
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
            disabled={$view.payload?.plan.limitExceeded || $view.payload?.replanning}
            aria-busy={$view.payload?.replanning}
            onclick={() => onConfirm({ encrypt, extract, ...(canMerge && merge ? { folderMerge: fileConflict } : {}) })}
        >
            {$view.payload?.replanning ? 'Checking…' : $view.payload?.queued ? 'Add to queue' : 'Import'}
        </button>
    {/snippet}
</ModalShell>
