import { afterEach, describe, expect, it, vi } from 'vitest';

const cancel = vi.hoisted(() => vi.fn());
vi.mock('../api', () => ({ cancelBiometricUnlock: cancel }));
import { cancelBiometricAuthentication, waitForBiometricCancellation } from './biometric';

afterEach(async () => {
    await waitForBiometricCancellation();
    vi.restoreAllMocks();
    cancel.mockReset();
});

describe('biometric cancellation handoff', () => {
    it('waits for every queued cancellation before permitting another prompt', async () => {
        let finish!: (value: { ok: true }) => void;
        cancel.mockReturnValueOnce(new Promise((done) => { finish = done; })).mockResolvedValue({ ok: true });
        cancelBiometricAuthentication();
        cancelBiometricAuthentication();
        const ready = vi.fn();
        const waiting = waitForBiometricCancellation().then(ready);
        expect(cancel).toHaveBeenCalledOnce();
        expect(ready).not.toHaveBeenCalled();
        finish({ ok: true });
        await waiting;
        expect(cancel).toHaveBeenCalledTimes(2);
        expect(ready).toHaveBeenCalledOnce();
    });

    it.each(['rejected', 'failed-result'])('reports %s cancellation safely and allows password recovery', async (failure) => {
        const warning = vi.spyOn(console, 'warn').mockImplementation(() => {});
        if (failure === 'rejected') cancel.mockRejectedValue(new Error('Private native diagnostic'));
        else cancel.mockResolvedValue({ ok: false, error: { code: 'biometric_failed', message: 'Private native diagnostic' } });
        cancelBiometricAuthentication();
        await waitForBiometricCancellation();
        expect(warning).toHaveBeenCalledWith('Could not cancel biometric authentication.');
        expect(warning).toHaveBeenCalledOnce();
    });
});
