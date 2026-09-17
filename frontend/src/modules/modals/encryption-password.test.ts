// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
    unlock: vi.fn(),
    loadStatus: vi.fn(),
    close: vi.fn(),
    setBusy: vi.fn(),
    setError: vi.fn(),
}));

vi.mock('../../api', () => ({ useEncryptionPassword: mocks.unlock }));
vi.mock('../encryption', () => ({ loadEncryptionStatus: mocks.loadStatus }));
vi.mock('../errors', () => ({ humanizeBackendError: (error: unknown) => String(error) }));
vi.mock('../../state', () => ({ state: { encryption: { loaded: true, hint: '' } } }));
vi.mock('../../ui/modals/encryption-password-modal-store', () => ({
    encryptionPasswordModal: {
        close: mocks.close,
        open: vi.fn(),
        setBusy: mocks.setBusy,
        setError: mocks.setError,
    },
}));

import { submitEncryptionPassword } from './encryption-password';

describe('encryption password submission', () => {
    beforeEach(() => {
        vi.clearAllMocks();
        mocks.unlock.mockResolvedValue({ ok: true });
        mocks.loadStatus.mockResolvedValue(undefined);
    });

    it('announces a successful unlock after refreshed encryption state is visible', async () => {
        const unlocked = vi.fn();
        window.addEventListener('tdrive:unlocked', unlocked, { once: true });

        await submitEncryptionPassword('secret');

        expect(mocks.loadStatus).toHaveBeenCalledOnce();
        expect(unlocked).toHaveBeenCalledOnce();
        expect(mocks.close).toHaveBeenCalledOnce();
    });

    it('does not announce an unlock when the password is rejected', async () => {
        mocks.unlock.mockResolvedValue({ ok: false, error: { code: 'invalid_encryption_password', message: 'Wrong password' } });
        const unlocked = vi.fn();
        window.addEventListener('tdrive:unlocked', unlocked, { once: true });

        await submitEncryptionPassword('wrong');

        expect(unlocked).not.toHaveBeenCalled();
        expect(mocks.close).not.toHaveBeenCalled();
    });
});
