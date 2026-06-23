// "Invite link" share modal — shows the t.me link with a Copy button.

import { installModalA11y } from './modal-a11y';

let a11y: ReturnType<typeof installModalA11y> | null = null;

export function setupShareDriveModal() {
    const modal = document.getElementById('share-drive-modal');
    const close = document.getElementById('share-drive-close');
    const copy = document.getElementById('share-drive-copy');
    const input = document.getElementById('share-drive-link') as HTMLInputElement | null;
    if (!modal || !close || !copy || !input) return;

    const dismiss = () => {
        a11y?.deactivate();
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

    a11y = installModalA11y(modal, {
        requestClose: dismiss,
        initialFocus: input,
        restoreFocus: '#drives-nav',
    });
}

export function openShareDriveModal(link: string, options: { approvalRequired?: boolean } = {}) {
    const modal = document.getElementById('share-drive-modal');
    const input = document.getElementById('share-drive-link') as HTMLInputElement | null;
    const subtitle = document.getElementById('share-drive-subtitle');
    if (!modal || !input) return;
    if (subtitle) {
        subtitle.textContent = options.approvalRequired
            ? 'People with this link can request access. An admin must approve them before they join.'
            : 'Anyone with this link can join the drive.';
    }
    input.value = String(link || '');
    modal.style.display = 'flex';
    a11y?.activate();
    requestAnimationFrame(() => input.select());
}
