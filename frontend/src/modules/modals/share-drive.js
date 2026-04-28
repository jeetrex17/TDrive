// "Invite link" share modal — shows the t.me link with a Copy button.

export function setupShareDriveModal() {
    const modal = document.getElementById('share-drive-modal');
    const close = document.getElementById('share-drive-close');
    const copy = document.getElementById('share-drive-copy');
    const input = document.getElementById('share-drive-link');
    if (!modal || !close || !copy || !input) return;

    const dismiss = () => {
        modal.style.display = 'none';
        input.value = '';
    };

    close.addEventListener('click', dismiss);
    modal.addEventListener('click', (e) => { if (e.target === modal) dismiss(); });

    copy.addEventListener('click', async () => {
        try {
            await navigator.clipboard.writeText(input.value);
            copy.textContent = 'Copied!';
            setTimeout(() => { copy.textContent = 'Copy link'; }, 1200);
        } catch (err) {
            input.select();
            document.execCommand('copy');
        }
    });

    input.addEventListener('keydown', (e) => {
        if (e.key === 'Escape') dismiss();
    });
}

export function openShareDriveModal(link) {
    const modal = document.getElementById('share-drive-modal');
    const input = document.getElementById('share-drive-link');
    if (!modal || !input) return;
    input.value = String(link || '');
    modal.style.display = 'flex';
    setTimeout(() => { input.focus(); input.select(); }, 0);
}
