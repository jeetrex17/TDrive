<script lang="ts">
    import { onDestroy, tick } from 'svelte';
    import ModalShell from './ModalShell.svelte';
    import { shareDriveModal } from './share-drive-modal-store';

    let inputEl = $state<HTMLInputElement | null>(null);
    let copied = $state(false);
    let copiedTimer: ReturnType<typeof setTimeout> | null = null;
    let wasOpen = false;

    const view = shareDriveModal.state;
    const link = $derived($view.payload?.link ?? '');
    const subtitle = $derived(
        $view.payload?.approvalRequired
            ? 'People with this link can request access. An admin must approve them before they join.'
            : 'Anyone with this link can join the drive.',
    );

    function close(): void {
        shareDriveModal.close();
    }

    async function copy(): Promise<void> {
        try {
            await navigator.clipboard.writeText(link);
        } catch {
            // Clipboard API can be unavailable in the webview; fall back to the
            // selection-based copy.
            inputEl?.select();
            document.execCommand('copy');
        }
        copied = true;
        if (copiedTimer) clearTimeout(copiedTimer);
        copiedTimer = setTimeout(() => {
            copied = false;
            copiedTimer = null;
        }, 1200);
    }

    $effect(() => {
        if ($view.open && !wasOpen) {
            copied = false;
            void tick().then(() => inputEl?.select());
        }
        wasOpen = $view.open;
    });

    onDestroy(() => {
        if (copiedTimer) clearTimeout(copiedTimer);
    });
</script>

<ModalShell
    hostId="share-drive-modal"
    open={$view.open}
    title="Invite link"
    titleId="share-drive-title"
    {subtitle}
    initialFocus="#share-drive-link"
    restoreFocus="#drives-nav"
    onClose={close}
>
    <input id="share-drive-link" type="text" readonly value={link} bind:this={inputEl} />

    {#snippet actions()}
        <button id="share-drive-close" class="secondary-btn" type="button" onclick={close}>Close</button>
        <button id="share-drive-copy" class="primary-btn" type="button" onclick={() => void copy()}>
            {copied ? 'Copied!' : 'Copy link'}
        </button>
    {/snippet}
</ModalShell>
