import { afterEach, describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import DeleteModal from './DeleteModal.svelte';
import EncryptionPasswordModal from './EncryptionPasswordModal.svelte';
import ImportOptionsModal from './ImportOptionsModal.svelte';
import JoinRequestsModal from './JoinRequestsModal.svelte';
import LogoutModal from './LogoutModal.svelte';
import MoveModal from './MoveModal.svelte';
import RenameModal from './RenameModal.svelte';
import UploadOptionsModal from './UploadOptionsModal.svelte';
import { closeDeleteModalView, openDeleteModalView } from './delete-modal-store';
import {
    closeRenameModalView,
    openRenameModalView,
    setRenameModalError,
    setRenameModalInFlight,
} from './rename-modal-store';
import { encryptionPasswordModal } from './encryption-password-modal-store';
import { importOptionsModal, type ImportOptionsPlan } from './import-options-modal-store';
import { joinRequestsList, joinRequestsModal } from './join-requests-modal-store';
import { logoutModal } from './logout-modal-store';
import { moveBrowse, moveModal, resetMoveBrowse } from './move-modal-store';
import { uploadOptionsModal } from './upload-options-modal-store';

const noop = () => {};

function makePlan(overrides: Partial<ImportOptionsPlan> = {}): ImportOptionsPlan {
    return {
        files: 3,
        folders: 1,
        bytes: 2048,
        oversize: 0,
        archives: 0,
        ignored: 0,
        maxItems: 10_000,
        limitExceeded: false,
        maxBytes: 1024 * 1024,
        errors: [],
        ...overrides,
    };
}

afterEach(() => {
    closeDeleteModalView();
    closeRenameModalView();
    encryptionPasswordModal.close();
    importOptionsModal.close();
    joinRequestsModal.close();
    joinRequestsList.set({ status: 'loading' });
    logoutModal.close();
    moveModal.close();
    resetMoveBrowse('');
    uploadOptionsModal.close();
});

describe('DeleteModal', () => {
    it('renders nothing while closed', () => {
        const { body } = render(DeleteModal, { props: { onConfirm: () => {} } });
        expect(body).not.toContain('modal-card');
    });

    it('renders the pre-computed copy and a danger confirm', () => {
        openDeleteModalView({
            title: 'Delete file?',
            itemName: 'notes.txt',
            subtitle: "This will remove the file from your Telegram channel. The action can't be undone.",
            confirmLabel: 'Delete file',
        });

        const { body } = render(DeleteModal, { props: { onConfirm: () => {} } });

        expect(body).toContain('Delete file?');
        expect(body).toContain('class="delete-target-name"');
        expect(body).toContain('notes.txt');
        expect(body).toContain('This will remove the file from your Telegram channel.');
        expect(body).toContain('danger-btn');
        expect(body).toContain('>Delete file</button>');
        expect(body).toContain('aria-labelledby="delete-modal-title"');
    });

    it('escapes untrusted names in the item label', () => {
        openDeleteModalView({
            title: 'Delete file?',
            itemName: '<img src=x>',
            subtitle: 'sub',
            confirmLabel: 'Delete',
        });

        const { body } = render(DeleteModal, { props: { onConfirm: () => {} } });

        expect(body).not.toContain('<img src=x>');
    });

    it('keeps long delete targets out of the heading', () => {
        const longName = '@Jesseverse_The_Mentalist_S02E05_720p_WEB_DL_x264_350MB_PaHe_in.mkv';
        openDeleteModalView({
            title: 'Delete file?',
            itemName: longName,
            subtitle: 'sub',
            confirmLabel: 'Delete',
        });

        const { body } = render(DeleteModal, { props: { onConfirm: () => {} } });

        expect(body).toContain('<h3 id="delete-modal-title" class="modal-title">Delete file?</h3>');
        expect(body).toContain(longName);
    });
});

describe('RenameModal', () => {
    it('renders folder copy for folder targets', () => {
        openRenameModalView({ type: 'folder', id: 'd:1', name: 'Docs' });

        const { body } = render(RenameModal, { props: { onSubmit: () => {} } });

        expect(body).toContain('Rename folder');
        expect(body).toContain('Choose a new folder name.');
        expect(body).toContain('id="rename-input"');
    });

    it('renders the store error inline', () => {
        openRenameModalView({ type: 'file', id: 42, name: 'a.txt' });
        setRenameModalError("Name can't include / or \\.");

        const { body } = render(RenameModal, { props: { onSubmit: () => {} } });

        expect(body).toContain('class="modal-error"');
        expect(body).toContain('include / or');
    });

    it('disables the controls while a rename is in flight', () => {
        openRenameModalView({ type: 'file', id: 42, name: 'a.txt' });
        setRenameModalInFlight(true);

        const { body } = render(RenameModal, { props: { onSubmit: () => {} } });

        const disabledCount = (body.match(/disabled/g) || []).length;
        expect(disabledCount).toBeGreaterThanOrEqual(3); // input + both buttons
    });
});

describe('LogoutModal', () => {
    it('renders both modes with a danger confirm', () => {
        logoutModal.open(null);

        const { body } = render(LogoutModal, { props: { onConfirm: noop } });

        expect(body).toContain('Quick logout');
        expect(body).toContain('Log out and reset');
        expect(body).toContain('class="primary-btn danger"');
    });
});

describe('UploadOptionsModal', () => {
    it('pluralizes the summary from the payload', () => {
        uploadOptionsModal.open({ count: 1 });
        expect(render(UploadOptionsModal, { props: { onCancel: noop, onConfirm: noop } }).body)
            .toContain('Upload 1 file');

        uploadOptionsModal.open({ count: 4 });
        expect(render(UploadOptionsModal, { props: { onCancel: noop, onConfirm: noop } }).body)
            .toContain('Upload 4 files');
    });
});

describe('EncryptionPasswordModal', () => {
    it('shows the hint row only when a hint exists', () => {
        encryptionPasswordModal.open({ hint: 'rhymes with cat' });
        expect(render(EncryptionPasswordModal, { props: { onCancel: noop, onSubmit: noop } }).body)
            .toContain('rhymes with cat');

        encryptionPasswordModal.open({ hint: '' });
        expect(render(EncryptionPasswordModal, { props: { onCancel: noop, onSubmit: noop } }).body)
            .not.toContain('encryption-password-hint');
    });

    it('renders the store error inline', () => {
        encryptionPasswordModal.open({ hint: '' });
        encryptionPasswordModal.setError('wrong password');

        const { body } = render(EncryptionPasswordModal, { props: { onCancel: noop, onSubmit: noop } });

        expect(body).toContain('class="modal-error"');
        expect(body).toContain('wrong password');
    });
});

describe('ImportOptionsModal', () => {
    it('derives the summary and hides rows that do not apply', () => {
        importOptionsModal.open({ plan: makePlan(), personal: false, hasArchives: false });

        const { body } = render(ImportOptionsModal, {
            props: { onCancel: noop, onConfirm: noop, onToggle: noop },
        });

        expect(body).toContain('3 files');
        expect(body).toContain('1 folder');
        expect(body).toContain('2.0 KB');
        expect(body).not.toContain('import-options-extract-row');
        expect(body).not.toContain('import-options-encrypt-row');
    });

    it('shows the oversize note and option rows when they apply', () => {
        importOptionsModal.open({
            plan: makePlan({ archives: 2, oversize: 1 }),
            personal: true,
            hasArchives: true,
        });

        const { body } = render(ImportOptionsModal, {
            props: { onCancel: noop, onConfirm: noop, onToggle: noop },
        });

        expect(body).toContain('2 archives');
        expect(body).toContain('1 file over 1.0 MB will be skipped');
        expect(body).toContain('import-options-extract-row');
        expect(body).toContain('import-options-encrypt-row');
    });

    it('explains when generated and cache entries will be skipped', () => {
        importOptionsModal.open({
            plan: makePlan({ ignored: 137 }),
            personal: false,
            hasArchives: false,
        });

        const { body } = render(ImportOptionsModal, {
            props: { onCancel: noop, onConfirm: noop, onToggle: noop },
        });

        expect(body).toContain('137 generated/cache entries will be skipped');
    });

    it('blocks an oversized import with actionable guidance', () => {
        importOptionsModal.open({
            plan: makePlan({ files: 10_001, maxItems: 10_000, limitExceeded: true }),
            personal: false,
            hasArchives: false,
        });

        const { body } = render(ImportOptionsModal, {
            props: { onCancel: noop, onConfirm: noop, onToggle: noop },
        });
        const confirm = body.match(/<button[^>]*id="import-options-confirm"[^>]*>/)?.[0] ?? '';

        expect(body).toContain('Keep it under 10,000 items');
        expect(body).toContain('removing generated/cache folders or splitting it into smaller batches');
        expect(confirm).toContain('disabled');
    });

    it('keeps the confirm action enabled for imports within the limit', () => {
        importOptionsModal.open({
            plan: makePlan({ files: 9_999, maxItems: 10_000, limitExceeded: false }),
            personal: false,
            hasArchives: false,
        });

        const { body } = render(ImportOptionsModal, {
            props: { onCancel: noop, onConfirm: noop, onToggle: noop },
        });
        const confirm = body.match(/<button[^>]*id="import-options-confirm"[^>]*>/)?.[0] ?? '';

        expect(confirm).not.toContain('disabled');
    });

    it('prevents confirmation while an option replan is pending', () => {
        importOptionsModal.open({
            plan: makePlan(),
            personal: true,
            hasArchives: true,
            replanning: true,
        });

        const { body } = render(ImportOptionsModal, {
            props: { onCancel: noop, onConfirm: noop, onToggle: noop },
        });
        const confirm = body.match(/<button[^>]*id="import-options-confirm"[^>]*>/)?.[0] ?? '';

        expect(confirm).toContain('disabled');
        expect(confirm).toContain('aria-busy="true"');
        expect(body).toContain('Checking…');
    });
});

describe('JoinRequestsModal', () => {
    it('renders loading, empty, and populated list states', () => {
        joinRequestsModal.open({ driveId: 7, title: 'Team Drive' });

        joinRequestsList.set({ status: 'loading' });
        expect(render(JoinRequestsModal, { props: { onAction: noop } }).body)
            .toContain('Loading requests...');

        joinRequestsList.set({ status: 'ready', rows: [], actingUserId: 0 });
        expect(render(JoinRequestsModal, { props: { onAction: noop } }).body)
            .toContain('No pending requests.');

        joinRequestsList.set({
            status: 'ready',
            rows: [{ userId: 42, displayName: 'Ada L.', username: 'ada', requestedAt: 0 }],
            actingUserId: 0,
        });
        const { body } = render(JoinRequestsModal, { props: { onAction: noop } });
        expect(body).toContain('Pending requests for Team Drive.');
        expect(body).toContain('Ada L.');
        expect(body).toContain('>Approve</button>');
        expect(body).toContain('>Reject</button>');
    });
});

describe('MoveModal', () => {
    it('renders the browse state and disables invalid destinations', () => {
        resetMoveBrowse('d:source-parent');
        moveModal.open({ title: 'Move "a.txt"' });
        moveBrowse.update((browse) => ({
            ...browse,
            path: [{ id: 'd:docs', name: 'Docs' }],
            listing: {
                status: 'ready',
                folders: [
                    { id: 'd:blocked', name: 'Blocked subtree' },
                    { id: 'd:open', name: 'Open me' },
                ],
            },
            blocked: new Set(['d:blocked']),
        }));

        const { body } = render(MoveModal, {
            props: { onOpenFolder: noop, onCrumb: noop, onBack: noop, onConfirm: noop },
        });

        expect(body).toContain('Move "a.txt"');
        expect(body).toContain('move-modal-card');
        expect(body).toContain('move-modal-footer');
        expect(body).toContain('Move to "Docs"');
        expect(body).toContain('Open me');
        expect(body).toContain('is-disabled');
    });

    it('renders the loading state at the root', () => {
        resetMoveBrowse('');
        moveModal.open({ title: 'Move 2 items' });

        const { body } = render(MoveModal, {
            props: { onOpenFolder: noop, onCrumb: noop, onBack: noop, onConfirm: noop },
        });

        expect(body).toContain('Move 2 items');
        expect(body).toContain('Loading folders...');
        expect(body).toContain('My Drive');
    });
});
