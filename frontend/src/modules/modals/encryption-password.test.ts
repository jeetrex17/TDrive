// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';

const mocks = vi.hoisted(() => ({
    unlock: vi.fn(), biometricStatus: vi.fn(), biometricUnlock: vi.fn(), biometricCancel: vi.fn(), loadStatus: vi.fn(),
}));
vi.mock('../../api', () => ({
    useEncryptionPassword: mocks.unlock, getBiometricStatus: mocks.biometricStatus,
    useBiometricUnlock: mocks.biometricUnlock, cancelBiometricUnlock: mocks.biometricCancel,
}));
vi.mock('../encryption', () => ({ loadEncryptionStatus: mocks.loadStatus }));
vi.mock('../../state', () => ({ state: { encryption: { loaded: true, hint: 'A useful hint' } } }));

import { cancelEncryptionPassword, callWithPasswordRetry, openEncryptionPasswordModal, submitEncryptionBiometric, submitEncryptionPassword } from './encryption-password';
import { encryptionPasswordModal } from '../../ui/modals/encryption-password-modal-store';
import { state } from '../../state';

const enrolled = { available: true, enrolled: true, kind: 'touch_id', reason: '' };
function deferred<T>() {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((done) => { resolve = done; });
    return { promise, resolve };
}
async function settle(): Promise<void> { for (let i = 0; i < 6; i++) await Promise.resolve(); }

describe('encryption session unlock', () => {
    beforeEach(() => {
        cancelEncryptionPassword();
        vi.resetAllMocks();
        mocks.unlock.mockResolvedValue({ ok: true });
        mocks.biometricStatus.mockResolvedValue(enrolled);
        mocks.biometricUnlock.mockResolvedValue({ ok: true });
        mocks.biometricCancel.mockResolvedValue({ ok: true });
        mocks.loadStatus.mockResolvedValue(undefined);
        state.encryption = { available: true, loaded: true, passwordSet: true, passwordRemembered: false, hint: 'A useful hint' };
    });
    afterEach(() => { cancelEncryptionPassword(); vi.restoreAllMocks(); });

    it('announces password unlock after the refreshed encryption state is visible', async () => {
        const unlocked = vi.spyOn(window, 'dispatchEvent');
        const result = openEncryptionPasswordModal();
        await settle();
        await submitEncryptionPassword('secret');
        expect(mocks.loadStatus).toHaveBeenCalledOnce();
        expect(unlocked).toHaveBeenCalledWith(expect.objectContaining({ type: 'tdrive:unlocked' }));
        expect(await result).toBe(true);
        expect(get(encryptionPasswordModal.state).open).toBe(false);
    });

    it('keeps password fallback available after a rejected password', async () => {
        mocks.unlock.mockResolvedValue({ ok: false, error: { code: 'invalid_encryption_password', message: 'Wrong password' } });
        const unlocked = vi.spyOn(window, 'dispatchEvent');
        void openEncryptionPasswordModal();
        await settle();
        await submitEncryptionPassword('wrong');
        expect(unlocked).not.toHaveBeenCalled();
        expect(get(encryptionPasswordModal.state)).toMatchObject({ open: true, busy: false, error: 'That encryption password is incorrect.' });
    });

    it('does not invoke the backend for an empty password or a dismissed prompt', async () => {
        const prompt = openEncryptionPasswordModal();
        await settle();
        await submitEncryptionPassword('');
        expect(get(encryptionPasswordModal.state).error).toBe('Enter your encryption password.');
        expect(mocks.unlock).not.toHaveBeenCalled();
        cancelEncryptionPassword();
        expect(await prompt).toBe(false);
        await submitEncryptionPassword('late-password');
        await submitEncryptionBiometric();
        expect(mocks.unlock).not.toHaveBeenCalled();
        expect(mocks.biometricUnlock).not.toHaveBeenCalled();
    });

    it('shows bridge failures inline without closing password recovery', async () => {
        mocks.unlock.mockRejectedValue(new Error('Backend unavailable'));
        void openEncryptionPasswordModal();
        await settle();
        await submitEncryptionPassword('secret');
        expect(get(encryptionPasswordModal.state)).toMatchObject({ open: true, busy: false });
        expect(get(encryptionPasswordModal.state).error).not.toBe('');
        mocks.biometricUnlock.mockRejectedValue({ code: 'biometric_failed', message: 'Private native error' });
        await submitEncryptionBiometric();
        expect(get(encryptionPasswordModal.state)).toMatchObject({ open: true, busy: false, error: 'Biometric authentication failed. Try again or use your encryption password.' });
    });

    it('does not complete an encrypted operation from a late password result', async () => {
        const nativeResult = deferred<{ ok: true }>();
        mocks.unlock.mockReturnValue(nativeResult.promise);
        const prompt = openEncryptionPasswordModal();
        await settle();
        const operation = submitEncryptionPassword('password');
        cancelEncryptionPassword();
        nativeResult.resolve({ ok: true });
        await operation;
        expect(await prompt).toBe(false);
        expect(mocks.loadStatus).not.toHaveBeenCalled();
    });

    it('coalesces encrypted callers into one native prompt and unlocks their session', async () => {
        const first = openEncryptionPasswordModal();
        const second = openEncryptionPasswordModal();
        await settle();
        expect(mocks.biometricStatus).toHaveBeenCalledOnce();
        expect(mocks.biometricUnlock).not.toHaveBeenCalled();
        await submitEncryptionBiometric();
        expect(await first).toBe(true);
        expect(await second).toBe(true);
        expect(mocks.biometricUnlock).toHaveBeenCalledOnce();
        expect(mocks.unlock).not.toHaveBeenCalled();
        expect(mocks.loadStatus).toHaveBeenCalledOnce();
    });

    it('keeps the password prompt usable when biometric status is unavailable', async () => {
        mocks.biometricStatus.mockRejectedValue(new Error('Native capability unavailable'));
        const prompt = openEncryptionPasswordModal();
        await settle();
        expect(get(encryptionPasswordModal.state)).toMatchObject({ open: true, busy: false, error: '' });
        await submitEncryptionPassword('secret');
        expect(await prompt).toBe(true);
        expect(mocks.biometricUnlock).not.toHaveBeenCalled();
    });

    it('refreshes the hint before showing the prompt and keeps password fallback after refresh failure', async () => {
        state.encryption = { ...state.encryption, hint: '', loaded: false };
        const prompt = openEncryptionPasswordModal();
        await settle();
        expect(mocks.loadStatus).toHaveBeenCalledOnce();
        expect(get(encryptionPasswordModal.state).open).toBe(true);
        cancelEncryptionPassword();
        expect(await prompt).toBe(false);
        mocks.loadStatus.mockRejectedValueOnce(new Error('Offline'));
        void openEncryptionPasswordModal();
        await settle();
        expect(get(encryptionPasswordModal.state)).toMatchObject({ open: true, payload: { hint: '' } });
    });

    it.each([
        ['biometric_canceled', 'Biometric unlock was canceled. Enter your password or try again.'],
        ['biometric_invalidated', 'Biometric enrollment changed. Enter your password, then enable biometrics again in encryption settings.'],
        ['biometric_locked_out', 'Biometrics are temporarily locked. Unlock your device or enter your encryption password.'],
    ])('preserves password recovery after %s without retrying the native prompt', async (code, message) => {
        mocks.biometricUnlock.mockResolvedValue({ ok: false, error: { code, message: 'Native diagnostic must not be displayed' } });
        const prompt = openEncryptionPasswordModal();
        await settle();
        await submitEncryptionBiometric();
        expect(get(encryptionPasswordModal.state)).toMatchObject({ open: true, busy: false, error: message });
        expect(mocks.biometricUnlock).toHaveBeenCalledOnce();
        await submitEncryptionPassword('secret');
        expect(await prompt).toBe(true);
    });

    it('cancels a native prompt immediately and ignores its late success after reopen', async () => {
        const response = deferred<{ ok: true }>();
        mocks.biometricUnlock.mockReturnValue(response.promise);
        const unlocked = vi.spyOn(window, 'dispatchEvent');
        const oldPrompt = openEncryptionPasswordModal();
        await settle();
        const operation = submitEncryptionBiometric();
        await settle();
        expect(get(encryptionPasswordModal.state).busy).toBe(true);
        cancelEncryptionPassword();
        expect(await oldPrompt).toBe(false);
        expect(mocks.biometricCancel).toHaveBeenCalledOnce();
        const nextPrompt = openEncryptionPasswordModal();
        await settle();
        response.resolve({ ok: true });
        await operation;
        expect(get(encryptionPasswordModal.state)).toMatchObject({ open: true, busy: false, error: '' });
        expect(mocks.loadStatus).not.toHaveBeenCalled();
        expect(unlocked).not.toHaveBeenCalled();
        cancelEncryptionPassword();
        expect(await nextPrompt).toBe(false);
    });

    it('ignores status that returns after the prompt was canceled', async () => {
        const status = deferred<typeof enrolled>();
        mocks.biometricStatus.mockReturnValue(status.promise);
        const prompt = openEncryptionPasswordModal();
        await settle();
        cancelEncryptionPassword();
        status.resolve(enrolled);
        await settle();
        expect(await prompt).toBe(false);
        expect(get(encryptionPasswordModal.state).open).toBe(false);
    });

    it('does not start biometrics before enrollment or repeat while a native prompt is active', async () => {
        mocks.biometricStatus.mockResolvedValue({ ...enrolled, enrolled: false });
        void openEncryptionPasswordModal();
        await settle();
        await submitEncryptionBiometric();
        expect(mocks.biometricUnlock).not.toHaveBeenCalled();
        cancelEncryptionPassword();
        const response = deferred<{ ok: true }>();
        mocks.biometricStatus.mockResolvedValue(enrolled);
        mocks.biometricUnlock.mockReturnValue(response.promise);
        void openEncryptionPasswordModal();
        await settle();
        const operation = submitEncryptionBiometric();
        await settle();
        await submitEncryptionBiometric();
        expect(mocks.biometricUnlock).toHaveBeenCalledOnce();
        response.resolve({ ok: true });
        await operation;
    });

    it('waits for delayed cancellation before opening another native prompt', async () => {
        const nativeResult = deferred<{ ok: true }>();
        const cancellation = deferred<{ ok: true }>();
        mocks.biometricUnlock.mockReturnValueOnce(nativeResult.promise).mockResolvedValue({ ok: true });
        mocks.biometricCancel.mockReturnValue(cancellation.promise);
        void openEncryptionPasswordModal();
        await settle();
        const oldOperation = submitEncryptionBiometric();
        await settle();
        expect(mocks.biometricUnlock).toHaveBeenCalledOnce();
        cancelEncryptionPassword();
        const newPrompt = openEncryptionPasswordModal();
        await settle();
        const newOperation = submitEncryptionBiometric();
        await settle();
        expect(mocks.biometricUnlock).toHaveBeenCalledOnce();
        cancellation.resolve({ ok: true });
        await newOperation;
        expect(mocks.biometricUnlock).toHaveBeenCalledTimes(2);
        expect(await newPrompt).toBe(true);
        nativeResult.resolve({ ok: true });
        await oldOperation;
    });

    it('retries a locked operation exactly once after biometric unlock', async () => {
        const call = vi.fn()
            .mockResolvedValueOnce({ ok: false, error: { code: 'encryption_password_required', message: 'Localized lock message' } })
            .mockResolvedValueOnce({ ok: true });
        const operation = callWithPasswordRetry(call);
        await settle();
        await submitEncryptionBiometric();
        expect(await operation).toEqual({ ok: true });
        expect(call).toHaveBeenCalledTimes(2);
    });

    it('does not retry protected work after cancel or prompt for unrelated operation failures', async () => {
        const call = vi.fn().mockResolvedValue({ ok: false, error: { code: 'encryption_password_required', message: 'Locked' } });
        const operation = callWithPasswordRetry(call);
        await settle();
        cancelEncryptionPassword();
        expect(await operation).toMatchObject({ ok: false, error: { code: 'canceled' } });
        expect(call).toHaveBeenCalledOnce();
        const unrelated = { ok: false as const, error: { code: 'permission_denied' as const, message: 'Denied' } };
        await expect(callWithPasswordRetry(async () => unrelated)).resolves.toEqual(unrelated);
        await expect(callWithPasswordRetry(async () => ({ ok: true }))).resolves.toEqual({ ok: true });
        expect(get(encryptionPasswordModal.state).open).toBe(false);
    });
});
