// "How would you like to upload?" — the single decision point for
// per-batch encryption. Resolves to { encrypt: boolean } on Continue,
// or null on Cancel.
//
// Sticky in-memory across the session: once the user picks an option,
// future opens default to that. We never persist it — security-relevant
// defaults shouldn't survive a restart.

let pending = null;
let lastChoice = 'plain'; // 'plain' | 'encrypt'

export function setupUploadOptionsModal() {
    const modal = document.getElementById('upload-options-modal');
    if (!modal) return;
    const cancel = modal.querySelector('#upload-options-cancel');
    const confirm = modal.querySelector('#upload-options-confirm');

    const finish = (result) => {
        modal.style.display = 'none';
        if (pending) {
            const resolve = pending;
            pending = null;
            resolve(result);
        }
    };

    cancel.addEventListener('click', () => finish(null));
    modal.addEventListener('click', (e) => { if (e.target === modal) finish(null); });

    confirm.addEventListener('click', () => {
        const selected = modal.querySelector('input[name="upload-mode"]:checked');
        const value = selected?.value === 'encrypt' ? 'encrypt' : 'plain';
        lastChoice = value;
        finish({ encrypt: value === 'encrypt' });
    });
}

export function openUploadOptionsModal({ count }) {
    const modal = document.getElementById('upload-options-modal');
    if (!modal) return Promise.resolve(null);

    const summary = modal.querySelector('#upload-options-summary');
    if (summary) {
        summary.textContent = count === 1
            ? 'Upload 1 file'
            : `Upload ${count} files`;
    }

    // Pre-select last choice so the user's preference compounds.
    const radios = modal.querySelectorAll('input[name="upload-mode"]');
    radios.forEach((r) => { r.checked = r.value === lastChoice; });

    return new Promise((resolve) => {
        if (pending) {
            const prev = pending;
            pending = (r) => { prev(r); resolve(r); };
            return;
        }
        pending = resolve;
        modal.style.display = 'flex';
    });
}
