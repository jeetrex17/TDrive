import { beforeEach, describe, expect, it, vi } from 'vitest';
import { state } from '../state';

const mocks = vi.hoisted(() => ({ status: vi.fn(), password: vi.fn(), setup: vi.fn() }));
vi.mock('../api', () => ({ getEncryptionStatus: mocks.status }));
vi.mock('./modals/encryption-password', () => ({ openEncryptionPasswordModal: mocks.password }));
vi.mock('./modals/encryption-setup', () => ({ openEncryptionSetupModal: mocks.setup }));

import { requireEncryptionPassword } from './encryption';

describe('requireEncryptionPassword', () => {
    beforeEach(() => {
        vi.clearAllMocks();
        state.encryption = { available: false, passwordSet: false, passwordRemembered: false, hint: '', loaded: false };
    });

    it('opens first-time setup when backup needs encryption and no password exists', async () => {
        mocks.status.mockResolvedValue({ available: true, passwordSet: false, passwordRemembered: false, hint: '' });
        mocks.setup.mockResolvedValue(true);

        await expect(requireEncryptionPassword()).resolves.toBe(true);

        expect(mocks.setup).toHaveBeenCalledOnce();
        expect(mocks.password).not.toHaveBeenCalled();
    });

    it('opens the unlock prompt when a password already exists', async () => {
        mocks.status.mockResolvedValue({ available: true, passwordSet: true, passwordRemembered: false, hint: '' });
        mocks.password.mockResolvedValue(true);

        await expect(requireEncryptionPassword()).resolves.toBe(true);

        expect(mocks.password).toHaveBeenCalledOnce();
        expect(mocks.setup).not.toHaveBeenCalled();
    });
});
