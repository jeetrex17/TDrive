<script lang="ts">
    import ModalShell from './ModalShell.svelte';
    import { uploadOptionsModal } from './upload-options-modal-store';

    interface Props {
        onCancel: () => void;
        onConfirm: (choice: { encrypt: boolean }) => void;
    }

    let { onCancel, onConfirm }: Props = $props();
    let uploadMode = $state<'plain' | 'encrypt'>('plain');
    let wasOpen = false;

    const view = uploadOptionsModal.state;
    const summary = $derived(
        $view.payload?.count === 1 ? 'Upload 1 file' : `Upload ${$view.payload?.count ?? 0} files`,
    );

    function confirm(): void {
        onConfirm({ encrypt: uploadMode === 'encrypt' });
    }

    $effect(() => {
        if ($view.open && !wasOpen) {
            // Default to plain upload every time. Encryption is explicit per batch.
            uploadMode = 'plain';
        }
        wasOpen = $view.open;
    });
</script>

<ModalShell
    hostId="upload-options-modal"
    open={$view.open}
    title="How would you like to upload?"
    titleId="upload-options-title"
    subtitle={summary}
    initialFocus='input[name="upload-mode"]:checked'
    restoreFocus="#file-list"
    onClose={onCancel}
>
    <label class="modal-choice-row">
        <input type="radio" name="upload-mode" value="plain" bind:group={uploadMode} />
        <span>
            <strong>Upload</strong>
            <small>Files are stored on Telegram as-is.</small>
        </span>
    </label>
    <label class="modal-choice-row">
        <input type="radio" name="upload-mode" value="encrypt" bind:group={uploadMode} />
        <span>
            <strong>Encrypt before upload</strong>
            <small>TDrive encrypts file contents before sending them to Telegram. Names and folders stay visible.</small>
        </span>
    </label>

    {#snippet actions()}
        <button id="upload-options-cancel" class="secondary-btn" type="button" onclick={onCancel}>Cancel</button>
        <button id="upload-options-confirm" class="primary-btn" type="button" onclick={confirm}>Continue</button>
    {/snippet}
</ModalShell>
