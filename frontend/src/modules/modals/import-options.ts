// Import dialog: shown when a selection contains folders or archives. It
// confirms the summary, lets the user choose Extract vs keep-as-file for
// archives, optional folder merge, and (on My Drive) encryption. Toggling
// encryption or archive extraction re-plans the selection.

import {
    importOptionsModal,
    type ImportChoice,
    type ImportOptionsPayload,
    type ImportOptionsPlan,
} from '../../ui/modals/import-options-modal-store';

export type ImportPlan = ImportOptionsPlan;

type ReplanFn = (encrypt: boolean, extract: boolean) => Promise<ImportPlan>;

let pending: ((result: ImportChoice | null) => void) | null = null;
let currentPayload: ImportOptionsPayload | null = null;
let currentReplan: ReplanFn | null = null;
let replanSeq = 0;

function finish(result: ImportChoice | null): void {
    importOptionsModal.close();
    currentPayload = null;
    currentReplan = null;
    replanSeq++;
    if (pending) {
        const resolve = pending;
        pending = null;
        resolve(result);
    }
}
export function cancelImportOptions(): void {
    finish(null);
}

export function confirmImportOptions(choice: ImportChoice): void {
    finish(choice);
}

export function replanImportOptions(encrypt: boolean, extract: boolean): void {
    void refreshPlan(encrypt, extract);
}


// refreshPlan re-plans after an option toggle so the summary counts stay
// accurate (e.g. extracting archives changes the file count and total bytes).
async function refreshPlan(encrypt: boolean, extract: boolean): Promise<void> {
    if (!currentReplan) return;
    const seq = ++replanSeq;
    if (currentPayload) {
        currentPayload = { ...currentPayload, replanning: true };
        importOptionsModal.setPayload(currentPayload);
    }
    try {
        const next = await currentReplan(encrypt, extract);
        if (seq !== replanSeq || !currentPayload) return; // ignore out-of-order responses
        currentPayload = { ...currentPayload, plan: next, replanning: false };
        importOptionsModal.setPayload(currentPayload);
    } catch {
        // Keep the last good summary if a re-plan fails, but never leave the
        // confirmation action stuck in its pending state.
        if (seq !== replanSeq || !currentPayload) return;
        currentPayload = { ...currentPayload, replanning: false };
        importOptionsModal.setPayload(currentPayload);
    }
}


export function openImportOptionsModal(opts: {
    plan: ImportPlan;
    personal: boolean;
    hasArchives: boolean;
    allowFolderMerge?: boolean;
    replan: ReplanFn;
    queued?: boolean;
    destination?: string;
    names?: readonly string[];
    encrypt?: boolean;
}): Promise<ImportChoice | null> {
    return new Promise((resolve) => {
        // A re-open while one is pending would strand the previous caller;
        // resolve it as canceled before the new prompt takes over.
        if (pending) {
            const prev = pending;
            pending = null;
            prev(null);
        }
        pending = resolve;
        currentReplan = opts.replan;
        replanSeq++;
        currentPayload = {
            plan: opts.plan,
            personal: opts.personal,
            hasArchives: opts.hasArchives,
            allowFolderMerge: opts.allowFolderMerge,
            replanning: false,
            queued: opts.queued,
            destination: opts.destination,
            names: opts.names,
            encrypt: opts.encrypt,
        };
        importOptionsModal.open(currentPayload);
    });
}
