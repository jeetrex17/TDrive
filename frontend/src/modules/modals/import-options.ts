// Import dialog: shown when a selection contains folders or archives. It
// confirms the summary, lets the user choose Extract vs keep-as-file for
// archives, and (on My Drive) Encrypt vs plain. Toggling either option re-plans
// so the counts stay accurate. Resolves to { encrypt, extract } or null.

import ImportOptionsModal from '../../ui/modals/ImportOptionsModal.svelte';
import {
    importOptionsModal,
    type ImportOptionsPayload,
    type ImportOptionsPlan,
} from '../../ui/modals/import-options-modal-store';
import { mountSvelte, type SvelteMountHandle } from '../../ui/mount';

export type ImportPlan = ImportOptionsPlan;

type ReplanFn = (encrypt: boolean, extract: boolean) => Promise<ImportPlan>;
type ImportChoice = { encrypt: boolean; extract: boolean };

let importOptionsModalHandle: SvelteMountHandle<Record<string, unknown>> | null = null;
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

// refreshPlan re-plans after an option toggle so the summary counts stay
// accurate (e.g. extracting archives changes the file count and total bytes).
async function refreshPlan(encrypt: boolean, extract: boolean): Promise<void> {
    if (!currentReplan) return;
    const seq = ++replanSeq;
    try {
        const next = await currentReplan(encrypt, extract);
        if (seq !== replanSeq || !currentPayload) return; // ignore out-of-order responses
        currentPayload = { ...currentPayload, plan: next };
        importOptionsModal.setPayload(currentPayload);
    } catch {
        // Keep the last good summary if a re-plan fails.
    }
}

export function setupImportOptionsModal() {
    const modal = document.getElementById('import-options-modal');
    if (!modal || importOptionsModalHandle) return;

    modal.replaceChildren();
    importOptionsModalHandle = mountSvelte(ImportOptionsModal, {
        target: modal,
        props: {
            onCancel: () => finish(null),
            onConfirm: (choice: ImportChoice) => finish(choice),
            onToggle: (encrypt: boolean, extract: boolean) => {
                void refreshPlan(encrypt, extract);
            },
        },
    });
}

export function openImportOptionsModal(opts: {
    plan: ImportPlan;
    personal: boolean;
    hasArchives: boolean;
    replan: ReplanFn;
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
        };
        importOptionsModal.open(currentPayload);
    });
}
