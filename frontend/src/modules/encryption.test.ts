import { beforeEach, describe, expect, it, vi } from 'vitest';
import { state } from '../state';

const mocks = vi.hoisted(() => ({ status: vi.fn(), password: vi.fn(), setup: vi.fn() }));
vi.mock('../api', () => ({ getEncryptionStatus: mocks.status }));
vi.mock('./modals/encryption-password', () => ({ openEncryptionPasswordModal: mocks.password }));
vi.mock('./modals/encryption-setup', () => ({ openEncryptionSetupModal: mocks.setup }));

import { loadEncryptionStatus, requireEncryptionPassword } from './encryption';

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

    it('retries an unavailable first lookup before prompting for an existing password', async () => {
        mocks.status
            .mockRejectedValueOnce(new Error('drive not ready'))
            .mockResolvedValueOnce({ available: true, passwordSet: true, passwordRemembered: false, hint: 'desktop hint' });
        mocks.password.mockResolvedValue(true);

        await expect(requireEncryptionPassword()).rejects.toThrow('drive not ready');
        expect(state.encryption.loaded).toBe(false);
        expect(mocks.setup).not.toHaveBeenCalled();

        await expect(requireEncryptionPassword()).resolves.toBe(true);
        expect(mocks.status).toHaveBeenCalledTimes(2);
        expect(mocks.password).toHaveBeenCalledOnce();
        expect(mocks.setup).not.toHaveBeenCalled();
    });

    it('refreshes a stale no-password snapshot before choosing a modal', async () => {
        state.encryption = { available: true, passwordSet: false, passwordRemembered: false, hint: '', loaded: true };
        mocks.status.mockResolvedValue({ available: true, passwordSet: true, passwordRemembered: false, hint: 'desktop hint' });
        mocks.password.mockResolvedValue(true);

        await expect(requireEncryptionPassword()).resolves.toBe(true);

        expect(mocks.status).toHaveBeenCalledOnce();
        expect(mocks.password).toHaveBeenCalledOnce();
        expect(mocks.setup).not.toHaveBeenCalled();
    });

    it('can unlock a known local vault when the status refresh fails', async () => {
        state.encryption = { available: true, passwordSet: true, passwordRemembered: false, hint: 'saved hint', loaded: true };
        mocks.status.mockRejectedValue(new Error('offline'));
        mocks.password.mockResolvedValue(true);

        await expect(requireEncryptionPassword()).resolves.toBe(true);

        expect(mocks.password).toHaveBeenCalledOnce();
        expect(mocks.setup).not.toHaveBeenCalled();
    });

    it('does not offer setup before a personal drive is available', async () => {
        mocks.status.mockResolvedValue({ available: false, passwordSet: false, passwordRemembered: false, hint: '' });

        await expect(requireEncryptionPassword()).rejects.toThrow('Encryption status is unavailable');

        expect(mocks.password).not.toHaveBeenCalled();
        expect(mocks.setup).not.toHaveBeenCalled();
    });

    it('does not use a cached password from a drive that is now unavailable', async () => {
        state.encryption = { available: true, passwordSet: true, passwordRemembered: false, hint: '', loaded: true };
        mocks.status.mockResolvedValue({ available: false, passwordSet: false, passwordRemembered: false, hint: '' });

        await expect(requireEncryptionPassword()).rejects.toThrow('Encryption status is unavailable');

        expect(mocks.password).not.toHaveBeenCalled();
        expect(mocks.setup).not.toHaveBeenCalled();
    });

    it('keeps passive status refresh best-effort after a transient failure', async () => {
        mocks.status.mockRejectedValue(new Error('offline'));

        await expect(loadEncryptionStatus()).resolves.toBeNull();

        expect(state.encryption.loaded).toBe(false);
    });
});
