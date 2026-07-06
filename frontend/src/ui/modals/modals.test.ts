import { afterEach, describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import DeleteModal from './DeleteModal.svelte';
import EncryptionPasswordModal from './EncryptionPasswordModal.svelte';
import ImportOptionsModal from './ImportOptionsModal.svelte';
import JoinRequestsModal from './JoinRequestsModal.svelte';
import LogoutModal from './LogoutModal.svelte';
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
import { uploadOptionsModal } from './upload-options-modal-store';

const noop = () => {};

function makePlan(overrides: Partial<ImportOptionsPlan> = {}): ImportOptionsPlan {
    return {
        files: 3,
        folders: 1,
        bytes: 2048,
        oversize: 0,
        archives: 0,
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
    uploadOptionsModal.close();
});

describe('DeleteModal', () => {
    it('renders nothing while closed', () => {
        const { body } = render(DeleteModal, { props: { onConfirm: () => {} } });
        expect(body).not.toContain('modal-card');
    });

    it('renders the pre-computed copy and a danger confirm', () => {
        openDeleteModalView({
            title: 'Delete "notes.txt"?',
            subtitle: "This will remove the file from your Telegram channel. The action can't be undone.",
            confirmLabel: 'Delete file',
        });

        const { body } = render(DeleteModal, { props: { onConfirm: () => {} } });

        expect(body).toContain('Delete "notes.txt"?');
        expect(body).toContain('This will remove the file from your Telegram channel.');
        expect(body).toContain('danger-btn');
        expect(body).toContain('>Delete file</button>');
        expect(body).toContain('aria-labelledby="delete-modal-title"');
    });

    it('escapes untrusted names in the title', () => {
        openDeleteModalView({
            title: 'Delete "<img src=x>"?',
            subtitle: 'sub',
            confirmLabel: 'Delete',
        });

        const { body } = render(DeleteModal, { props: { onConfirm: () => {} } });

        expect(body).not.toContain('<img src=x>');
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
