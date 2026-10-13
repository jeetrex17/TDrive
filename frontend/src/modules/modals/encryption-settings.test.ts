// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';

const mocks = vi.hoisted(() => ({ status: vi.fn(), enable: vi.fn(), disable: vi.fn(), cancel: vi.fn(), change: vi.fn(), loadStatus: vi.fn(), notify: vi.fn() }));
vi.mock('../../api', () => ({
    getBiometricStatus: mocks.status, enableBiometricUnlock: mocks.enable,
    disableBiometricUnlock: mocks.disable, cancelBiometricUnlock: mocks.cancel, changeEncryptionPassword: mocks.change,
}));
vi.mock('../encryption', () => ({ loadEncryptionStatus: mocks.loadStatus }));
vi.mock('../notifications', () => ({ notify: mocks.notify }));
vi.mock('../../state', () => ({ state: { encryption: { hint: 'My hint' } } }));

import { cancelEncryptionSettings, openEncryptionSettingsModal, submitBiometricEnrollment, removeBiometricEnrollment, submitEncryptionSettings } from './encryption-settings';
import { encryptionSettingsModal } from '../../ui/modals/encryption-settings-modal-store';

const available = { available: true, enrolled: false, kind: 'windows_hello', reason: '' };
async function settle() { for (let i = 0; i < 6; i++) await Promise.resolve(); }

describe('biometric enrollment settings', () => {
    beforeEach(() => {
        cancelEncryptionSettings();
        vi.resetAllMocks();
        mocks.status.mockResolvedValue(available);
        mocks.enable.mockResolvedValue({ ok: true });
        mocks.disable.mockResolvedValue({ ok: true });
        mocks.cancel.mockResolvedValue({ ok: true });
        mocks.change.mockResolvedValue({ ok: true });
        mocks.loadStatus.mockResolvedValue(undefined);
    });
    afterEach(() => cancelEncryptionSettings());

    it('requires a fresh password before device enrollment, even for an unlocked session', async () => {
        openEncryptionSettingsModal();
        await settle();
        await submitBiometricEnrollment('');
        expect(mocks.enable).not.toHaveBeenCalled();
        expect(get(encryptionSettingsModal.state).error).toBe('Enter your encryption password to enable biometrics on this device.');
        mocks.status.mockResolvedValue({ ...available, enrolled: true });
        await submitBiometricEnrollment('fresh-password');
        expect(mocks.enable).toHaveBeenCalledWith('fresh-password');
        expect(get(encryptionSettingsModal.state)).toMatchObject({ open: true, busy: false, payload: { biometric: { enrolled: true } } });
        expect(mocks.notify).toHaveBeenCalledWith(expect.objectContaining({ title: 'Biometric unlock enabled' }));
    });
    it('retains a safe password recovery path when enrollment fails', async () => {
        mocks.enable.mockResolvedValue({ ok: false, error: { code: 'biometric_failed', message: 'Private native diagnostics' } });
        openEncryptionSettingsModal();
        await settle();
        await submitBiometricEnrollment('fresh-password');
        expect(get(encryptionSettingsModal.state)).toMatchObject({ open: true, busy: false, error: 'Biometric authentication failed. Try again or use your encryption password.' });
        expect(mocks.notify).not.toHaveBeenCalled();
    });

    it.each([false, true])('reports enrollment cleanup failure after cancel without changing a replacement dialog (reopened: %s)', async (reopened) => {
        let finish!: (value: unknown) => void;
        mocks.enable.mockReturnValue(new Promise((done) => { finish = done; }));
        openEncryptionSettingsModal();
        await settle();
        const operation = submitBiometricEnrollment('password');
        await settle();
        cancelEncryptionSettings();
        if (reopened) {
            openEncryptionSettingsModal();
            await settle();
        }
        finish({ ok: false, error: { code: 'biometric_cleanup_required', message: 'Private native diagnostics' } });
        await operation;
        expect(get(encryptionSettingsModal.state)).toMatchObject({ open: reopened, error: '' });
        expect(mocks.notify).toHaveBeenCalledWith({
            level: 'error', title: 'Review biometric enrollment',
            body: 'Biometric enrollment could not be removed. Open encryption settings and disable biometric unlock before trying again.',
        });
    });

    it('refreshes remaining device enrollment and shows safe cleanup guidance in the current dialog', async () => {
        mocks.enable.mockResolvedValue({ ok: false, error: { code: 'biometric_cleanup_required', message: 'Private native diagnostics' } });
        openEncryptionSettingsModal();
        await settle();
        mocks.status.mockResolvedValue({ ...available, enrolled: true });
        await submitBiometricEnrollment('password');
        expect(get(encryptionSettingsModal.state)).toMatchObject({
            open: true, busy: false,
            error: 'Biometric enrollment could not be removed. Open encryption settings and disable biometric unlock before trying again.',
            payload: { biometric: { enrolled: true } },
        });
        expect(mocks.notify).toHaveBeenCalledOnce();
    });
    it('removes enrollment even when the biometric hardware is unavailable', async () => {
        mocks.status.mockResolvedValue({ ...available, available: false, enrolled: true, reason: 'locked_out' });
        openEncryptionSettingsModal();
        await settle();
        mocks.status.mockResolvedValue({ ...available, available: false });
        await removeBiometricEnrollment();
        expect(mocks.disable).toHaveBeenCalledOnce();
        expect(get(encryptionSettingsModal.state).payload?.biometric?.enrolled).toBe(false);
    });
    it('cancel closes enrollment while native authentication is pending and ignores a late result', async () => {
        let resolve!: (value: { ok: true }) => void;
        mocks.enable.mockReturnValue(new Promise((done) => { resolve = done; }));
        openEncryptionSettingsModal();
        await settle();
        const operation = submitBiometricEnrollment('fresh-password');
        await settle();
        cancelEncryptionSettings();
        expect(mocks.cancel).toHaveBeenCalledOnce();
        openEncryptionSettingsModal();
        await settle();
        resolve({ ok: true });
        await operation;
        expect(mocks.notify).not.toHaveBeenCalled();
        expect(get(encryptionSettingsModal.state)).toMatchObject({ open: true, busy: false });
    });

    it('keeps password settings available when capability discovery fails', async () => {
        mocks.status.mockRejectedValue(new Error('Native capability missing'));
        openEncryptionSettingsModal();
        await settle();
        expect(get(encryptionSettingsModal.state)).toMatchObject({ open: true, busy: false, payload: { biometric: { available: false, enrolled: false, reason: 'unavailable' } } });
        await submitBiometricEnrollment('password');
        await removeBiometricEnrollment();
        expect(mocks.enable).not.toHaveBeenCalled();
        expect(mocks.disable).not.toHaveBeenCalled();
        await submitEncryptionSettings('current', 'new-password', 'new-password', 'hint');
        expect(mocks.change).toHaveBeenCalledWith('current', 'new-password', 'hint');
        expect(get(encryptionSettingsModal.state).open).toBe(false);
    });

    it.each([
        ['', 'new-password', 'new-password', 'Enter your current password.'],
        ['current', 'short', 'short', 'Use at least 8 characters for the new password.'],
        ['current', 'new-password', 'different', 'New passwords do not match.'],
    ])('validates the current and new password before changing the vault', async (current, next, confirm, message) => {
        openEncryptionSettingsModal();
        await settle();
        await submitEncryptionSettings(current, next, confirm, 'hint');
        expect(mocks.change).not.toHaveBeenCalled();
        expect(get(encryptionSettingsModal.state).error).toBe(message);
    });

    it('keeps rejected password changes and biometric removal failures inline', async () => {
        mocks.status.mockResolvedValue({ ...available, enrolled: true });
        mocks.change.mockResolvedValue({ ok: false, error: { code: 'invalid_encryption_password', message: 'Private details' } });
        mocks.disable.mockResolvedValue({ ok: false, error: { code: 'biometric_failed', message: 'Private native details' } });
        openEncryptionSettingsModal();
        await settle();
        await submitEncryptionSettings('wrong', 'new-password', 'new-password', 'hint');
        expect(get(encryptionSettingsModal.state)).toMatchObject({ open: true, busy: false, error: 'That encryption password is incorrect.' });
        await removeBiometricEnrollment();
        expect(get(encryptionSettingsModal.state)).toMatchObject({ open: true, busy: false, error: 'Biometric authentication failed. Try again or use your encryption password.' });
        expect(get(encryptionSettingsModal.state).payload?.biometric?.enrolled).toBe(true);
        expect(mocks.notify).not.toHaveBeenCalled();
    });

    it('handles bridge rejection without pretending enrollment or password changes succeeded', async () => {
        mocks.enable.mockRejectedValue({ code: 'biometric_failed', message: 'Private details' });
        mocks.change.mockRejectedValue({ code: 'invalid_encryption_password', message: 'Private details' });
        openEncryptionSettingsModal();
        await settle();
        await submitBiometricEnrollment('password');
        expect(get(encryptionSettingsModal.state)).toMatchObject({ open: true, busy: false, error: 'Biometric authentication failed. Try again or use your encryption password.' });
        await submitEncryptionSettings('current', 'new-password', 'new-password', 'hint');
        expect(get(encryptionSettingsModal.state)).toMatchObject({ open: true, busy: false, error: 'That encryption password is incorrect.' });
        expect(mocks.notify).not.toHaveBeenCalled();
    });

    it('ignores discovery or password change completion after the settings dialog closes', async () => {
        let resolve!: (value: typeof available) => void;
        mocks.status.mockReturnValueOnce(new Promise((done) => { resolve = done; }));
        openEncryptionSettingsModal();
        cancelEncryptionSettings();
        resolve(available);
        await settle();
        expect(get(encryptionSettingsModal.state).open).toBe(false);
        let finish!: (value: { ok: true }) => void;
        mocks.change.mockReturnValueOnce(new Promise((done) => { finish = done; }));
        openEncryptionSettingsModal();
        await settle();
        const operation = submitEncryptionSettings('current', 'new-password', 'new-password', 'hint');
        cancelEncryptionSettings();
        finish({ ok: true });
        await operation;
        expect(get(encryptionSettingsModal.state).open).toBe(false);
        expect(mocks.notify).not.toHaveBeenCalled();
        await submitEncryptionSettings('current', 'new-password', 'new-password', 'hint');
        expect(mocks.change).toHaveBeenCalledOnce();
    });
});
