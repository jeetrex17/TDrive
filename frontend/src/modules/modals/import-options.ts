// Import dialog: shown when a selection contains folders or archives. It
// confirms the summary, lets the user choose Extract vs keep-as-file for
// archives, and (on My Drive) Encrypt vs plain. Toggling either option re-plans
// so the counts stay accurate. Resolves to { encrypt, extract } or null.

import { installModalA11y } from './modal-a11y';

export type ImportPlan = {
    files: number;
    folders: number;
    bytes: number;
    oversize: number;
    archives: number;
    maxBytes: number;
    errors?: string[];
};

type ReplanFn = (encrypt: boolean, extract: boolean) => Promise<ImportPlan>;
type ImportChoice = { encrypt: boolean; extract: boolean };

let pending: ((result: ImportChoice | null) => void) | null = null;
let currentReplan: ReplanFn | null = null;
let replanSeq = 0;
let a11y: ReturnType<typeof installModalA11y> | null = null;

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

function summaryText(plan: ImportPlan): string {
    const parts = [plural(plan.files, 'file', 'files')];
    if (plan.folders > 0) parts.push(plural(plan.folders, 'folder', 'folders'));
    if (plan.archives > 0) parts.push(plural(plan.archives, 'archive', 'archives'));
    let text = parts.join('  ·  ');
    if (plan.bytes > 0) text += `  ·  ${humanBytes(plan.bytes)}`;
    return text;
}

function applyPlan(modal: HTMLElement, plan: ImportPlan) {
    const summary = modal.querySelector('#import-options-summary');
    if (summary) summary.textContent = summaryText(plan);

    const note = modal.querySelector('#import-options-note') as HTMLElement | null;
    if (note) {
        const errors = Array.isArray(plan.errors) ? plan.errors.length : 0;
        const notes: string[] = [];
        if (plan.oversize > 0) {
            notes.push(`${plural(plan.oversize, 'file', 'files')} over ${humanBytes(plan.maxBytes)} will be skipped (Telegram's per-file limit).`);
        }
        if (errors > 0) {
            notes.push(`${plural(errors, 'item', 'items')} could not be scanned and may be skipped or uploaded as-is.`);
        }
        if (notes.length > 0) {
            note.textContent = notes.join(' ');
            note.style.display = 'block';
        } else {
            note.style.display = 'none';
        }
    }
}

export function setupImportOptionsModal() {
    const modal = document.getElementById('import-options-modal');
    if (!modal) return;
    const cancel = modal.querySelector('#import-options-cancel') as HTMLButtonElement | null;
    const confirm = modal.querySelector('#import-options-confirm') as HTMLButtonElement | null;
    const extract = modal.querySelector('#import-options-extract') as HTMLInputElement | null;
    const encrypt = modal.querySelector('#import-options-encrypt') as HTMLInputElement | null;

    const finish = (result: ImportChoice | null) => {
        a11y?.deactivate();
        modal.style.display = 'none';
        currentReplan = null;
        replanSeq++;
        if (pending) {
            const resolve = pending;
            pending = null;
            resolve(result);
        }
    };

    cancel?.addEventListener('click', () => finish(null));
    modal.addEventListener('click', (e) => { if (e.target === modal) finish(null); });
    confirm?.addEventListener('click', () => finish({
        encrypt: !!encrypt?.checked,
        extract: !!extract?.checked,
    }));

    const refresh = async () => {
        if (!currentReplan) return;
        const seq = ++replanSeq;
        try {
            const next = await currentReplan(!!encrypt?.checked, !!extract?.checked);
            if (seq === replanSeq) applyPlan(modal, next); // ignore out-of-order responses
        } catch {
            // Keep the last good summary if a re-plan fails.
        }
    };
    extract?.addEventListener('change', refresh);
    encrypt?.addEventListener('change', refresh);

    a11y = installModalA11y(modal, {
        requestClose: () => finish(null),
        initialFocus: () =>
            modal.querySelector('#import-options-extract-row input:not([disabled])') ||
            modal.querySelector('#import-options-encrypt-row input:not([disabled])') ||
            confirm,
        restoreFocus: '#file-list',
    });
}

export function openImportOptionsModal(opts: {
    plan: ImportPlan;
    personal: boolean;
    hasArchives: boolean;
    replan: ReplanFn;
}): Promise<ImportChoice | null> {
    const modal = document.getElementById('import-options-modal');
    if (!modal) return Promise.resolve(null);

    currentReplan = opts.replan;
    replanSeq++;

    const extractRow = modal.querySelector('#import-options-extract-row') as HTMLElement | null;
    const encryptRow = modal.querySelector('#import-options-encrypt-row') as HTMLElement | null;
    const extract = modal.querySelector('#import-options-extract') as HTMLInputElement | null;
    const encrypt = modal.querySelector('#import-options-encrypt') as HTMLInputElement | null;

    if (extract) extract.checked = false;
    if (encrypt) encrypt.checked = false;
    if (extractRow) extractRow.style.display = opts.hasArchives ? 'flex' : 'none';
    if (encryptRow) encryptRow.style.display = opts.personal ? 'flex' : 'none';
    applyPlan(modal, opts.plan);

    return new Promise((resolve) => {
        pending = resolve;
        modal.style.display = 'flex';
        a11y?.activate();
    });
}
