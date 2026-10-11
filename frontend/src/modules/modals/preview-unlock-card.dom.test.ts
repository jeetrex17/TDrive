import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const api = vi.hoisted(() => ({ useEncryptionPassword: vi.fn(), getBiometricStatus: vi.fn(), useBiometricUnlock: vi.fn(), cancelBiometricUnlock: vi.fn() }));
const encryption = vi.hoisted(() => ({ loadEncryptionStatus: vi.fn(async () => {}) }));

// The real success check comes along, because "a wrong password is an
// unsuccessful result rather than a rejection" is exactly what is under test.
vi.mock('../../api', async () => ({
    useEncryptionPassword: api.useEncryptionPassword,
    getBiometricStatus: api.getBiometricStatus,
    useBiometricUnlock: api.useBiometricUnlock,
    cancelBiometricUnlock: api.cancelBiometricUnlock,
    requireOperationSuccess: (await vi.importActual<typeof import('../../api/operation')>('../../api/operation')).requireOperationSuccess,
}));
vi.mock('../encryption', () => ({ loadEncryptionStatus: encryption.loadEncryptionStatus }));

import { createUnlockCard, type UnlockCard } from './preview-unlock-card';
import { waitForBiometricCancellation } from '../biometric';

const MARKUP = `
    <div id="card" style="display: none;">
        <button id="biometric" type="button" hidden><span id="biometric-label"></span></button>
        <input id="input" type="password">
        <button id="eye" type="button" aria-pressed="false" aria-label="Show password"></button>
        <button id="unlock" type="button"></button>
        <div id="error" style="display: none;"></div>
        <div id="hint" style="display: none;">Hint: <span id="hint-text"></span></div>
    </div>`;

let card: UnlockCard;
const byId = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;

function build(): UnlockCard {
    return createUnlockCard({
        card: byId('card'),
        input: byId<HTMLInputElement>('input'),
        unlockButton: byId<HTMLButtonElement>('unlock'),
        eyeButton: byId<HTMLButtonElement>('eye'),
        error: byId('error'),
        hint: byId('hint'),
        hintText: byId('hint-text'),
        biometricButton: byId<HTMLButtonElement>('biometric'),
        biometricLabel: byId('biometric-label'),
    });
}

beforeEach(() => {
    vi.resetAllMocks();
    api.useEncryptionPassword.mockResolvedValue({ ok: true });
    api.getBiometricStatus.mockResolvedValue({ available: true, enrolled: true, kind: 'touch_id', reason: '' });
    api.useBiometricUnlock.mockResolvedValue({ ok: true });
    api.cancelBiometricUnlock.mockResolvedValue({ ok: true });
    encryption.loadEncryptionStatus.mockResolvedValue(undefined);
    document.body.innerHTML = MARKUP;
    card = build();
    card.show('');
});

afterEach(async () => {
    card.hide();
    await waitForBiometricCancellation();
    vi.restoreAllMocks();
});

async function settle(): Promise<void> { for (let i = 0; i < 6; i++) await Promise.resolve(); }

function deferred<T>() {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((done) => { resolve = done; });
    return { promise, resolve };
}

describe('explicit biometric unlock', () => {
    it('offers Touch ID only after fresh enrolled capability arrives without prompting or taking focus', async () => {
        const status = deferred<{ available: boolean; enrolled: boolean; kind: string; reason: string }>();
        api.getBiometricStatus.mockReturnValueOnce(status.promise);
        card.show('');
        expect(byId<HTMLButtonElement>('biometric').hidden).toBe(true);
        status.resolve({ available: true, enrolled: true, kind: 'touch_id', reason: '' });
        await settle();
        expect(byId<HTMLButtonElement>('biometric').hidden).toBe(false);
        expect(byId('biometric-label').textContent).toBe('Use Touch ID');
        expect(api.useBiometricUnlock).not.toHaveBeenCalled();
        expect(document.activeElement).not.toBe(byId('input'));
    });

    it.each([
        { available: false, enrolled: true, kind: 'touch_id', reason: 'locked_out' },
        { available: true, enrolled: false, kind: 'biometric', reason: 'not_enrolled' },
    ])('keeps password fallback without a biometric action for %j', async (status) => {
        api.getBiometricStatus.mockResolvedValue(status);
        card.show('');
        await settle();
        expect(byId<HTMLButtonElement>('biometric').hidden).toBe(true);
        expect(byId<HTMLInputElement>('input').disabled).toBe(false);
        await card.submitBiometric(vi.fn());
        expect(api.useBiometricUnlock).not.toHaveBeenCalled();
    });

    it('clears typed secrets before native unlock, refreshes the session and reloads once', async () => {
        await settle();
        byId<HTMLInputElement>('input').value = 'unused-password';
        const onUnlocked = vi.fn();
        const event = vi.spyOn(window, 'dispatchEvent');
        const pending = card.submitBiometric(onUnlocked);
        expect(byId<HTMLInputElement>('input').value).toBe('');
        expect(byId<HTMLInputElement>('input').disabled).toBe(true);
        await card.submitBiometric(onUnlocked);
        await pending;
        expect(api.useBiometricUnlock).toHaveBeenCalledOnce();
        expect(api.useEncryptionPassword).not.toHaveBeenCalled();
        expect(encryption.loadEncryptionStatus).toHaveBeenCalledOnce();
        expect(event).toHaveBeenCalledWith(expect.objectContaining({ type: 'tdrive:unlocked' }));
        expect(onUnlocked).toHaveBeenCalledOnce();
    });

    it('keeps password recovery after biometric cancellation without automatically retrying', async () => {
        api.useBiometricUnlock.mockResolvedValue({ ok: false, error: { code: 'biometric_canceled', message: 'Native diagnostic' } });
        await settle();
        await card.submitBiometric(vi.fn());
        expect(byId('error').textContent).toBe('Biometric unlock was canceled. Enter your password or try again.');
        expect(byId<HTMLInputElement>('input').disabled).toBe(false);
        byId<HTMLInputElement>('input').value = 'fallback-password';
        const onUnlocked = vi.fn();
        await card.submit(onUnlocked);
        expect(api.useEncryptionPassword).toHaveBeenCalledWith('fallback-password');
        expect(onUnlocked).toHaveBeenCalledOnce();
        expect(api.useBiometricUnlock).toHaveBeenCalledOnce();
    });

    it('cancels native authentication on hide and ignores late success after a different photo is shown', async () => {
        const response = deferred<{ ok: true }>();
        api.useBiometricUnlock.mockReturnValue(response.promise);
        await settle();
        const onUnlocked = vi.fn();
        const event = vi.spyOn(window, 'dispatchEvent');
        const pending = card.submitBiometric(onUnlocked);
        await settle();
        card.hide();
        expect(api.cancelBiometricUnlock).toHaveBeenCalledOnce();
        card.show('new photo hint');
        await settle();
        response.resolve({ ok: true });
        await pending;
        expect(onUnlocked).not.toHaveBeenCalled();
        expect(event).not.toHaveBeenCalled();
        expect(encryption.loadEncryptionStatus).not.toHaveBeenCalled();
        expect(byId('hint-text').textContent).toBe('new photo hint');
        expect(byId<HTMLInputElement>('input').disabled).toBe(false);
    });

    it('discards old availability after the card is hidden or replaced by an unavailable device state', async () => {
        const oldStatus = deferred<{ available: boolean; enrolled: boolean; kind: string; reason: string }>();
        api.getBiometricStatus.mockReturnValueOnce(oldStatus.promise);
        card.show('');
        card.hide();
        api.getBiometricStatus.mockResolvedValue({ available: false, enrolled: false, kind: 'biometric', reason: 'unsupported' });
        card.show('');
        await settle();
        oldStatus.resolve({ available: true, enrolled: true, kind: 'touch_id', reason: '' });
        await settle();
        expect(byId<HTMLButtonElement>('biometric').hidden).toBe(true);
        expect(api.useBiometricUnlock).not.toHaveBeenCalled();
    });

    it('leaves password fallback usable when native capability discovery rejects', async () => {
        api.getBiometricStatus.mockRejectedValueOnce(new Error('Native bridge missing'));
        card.show('');
        await settle();
        expect(byId<HTMLButtonElement>('biometric').hidden).toBe(true);
        byId<HTMLInputElement>('input').value = 'password';
        const onUnlocked = vi.fn();
        await card.submit(onUnlocked);
        expect(onUnlocked).toHaveBeenCalledOnce();
    });

    it('waits for cancellation of the previous photo before starting another native prompt', async () => {
        const response = deferred<{ ok: true }>();
        const cancellation = deferred<{ ok: true }>();
        api.useBiometricUnlock.mockReturnValueOnce(response.promise).mockResolvedValue({ ok: true });
        api.cancelBiometricUnlock.mockReturnValue(cancellation.promise);
        await settle();
        const previous = card.submitBiometric(vi.fn());
        await settle();
        card.hide();
        card.show('new photo');
        await settle();
        const current = card.submitBiometric(vi.fn());
        await settle();
        expect(api.useBiometricUnlock).toHaveBeenCalledOnce();
        cancellation.resolve({ ok: true });
        await current;
        expect(api.useBiometricUnlock).toHaveBeenCalledTimes(2);
        response.resolve({ ok: true });
        await previous;
    });
});

describe('showing the unlock card', () => {
    it("shows the drive's hint when there is one and stays quiet when there is not", () => {
        card.show('my usual one');
        expect(byId('hint-text').textContent).toBe('my usual one');
        expect(byId('hint').style.display).toBe('block');

        card.show('');
        expect(byId('hint').style.display).toBe('none');
    });

    it('forgets the last attempt, so a reopened card never shows a stale failure', () => {
        byId<HTMLInputElement>('input').value = 'wrong';
        byId('error').textContent = 'Incorrect password';
        byId('error').style.display = 'block';

        card.show('');

        expect(byId<HTMLInputElement>('input').value).toBe('');
        expect(byId('error').style.display).toBe('none');
        expect(byId('error').textContent).toBe('');
    });

    it('starts with the password hidden even if it was revealed last time', () => {
        card.toggleReveal();
        expect(byId<HTMLInputElement>('input').type).toBe('text');

        card.show('');

        expect(byId<HTMLInputElement>('input').type).toBe('password');
        expect(byId('eye').getAttribute('aria-pressed')).toBe('false');
        expect(byId('eye').getAttribute('aria-label')).toBe('Show password');
    });

    it('hides the pill without touching anything else the preview is showing', () => {
        card.show('');
        card.hide();
        expect(byId('card').style.display).toBe('none');
    });
});

describe('revealing the typed password', () => {
    it('says which way the toggle now goes', () => {
        card.toggleReveal();
        expect(byId<HTMLInputElement>('input').type).toBe('text');
        expect(byId('eye').getAttribute('aria-pressed')).toBe('true');
        expect(byId('eye').getAttribute('aria-label')).toBe('Hide password');

        card.toggleReveal();
        expect(byId<HTMLInputElement>('input').type).toBe('password');
        expect(byId('eye').getAttribute('aria-label')).toBe('Show password');
    });

    it('leaves the reader typing in the field rather than on the toggle', () => {
        card.toggleReveal();
        expect(document.activeElement).toBe(byId('input'));
    });
});

describe('submitting a password', () => {
    it('asks for one instead of sending an empty attempt', async () => {
        await card.submit(() => {});
        expect(api.useEncryptionPassword).not.toHaveBeenCalled();
        expect(byId('error').textContent).toBe('Enter your encryption password.');
        expect(byId('error').style.display).toBe('block');
    });

    it('unlocks, clears the field and tells the caller to reload its photo', async () => {
        const unlocked = vi.fn();
        const listener = vi.fn();
        window.addEventListener('tdrive:unlocked', listener);
        byId<HTMLInputElement>('input').value = 'correct horse';

        await card.submit(unlocked);

        expect(api.useEncryptionPassword).toHaveBeenCalledWith('correct horse');
        expect(encryption.loadEncryptionStatus).toHaveBeenCalled();
        expect(listener).toHaveBeenCalled();
        expect(unlocked).toHaveBeenCalled();
        expect(byId<HTMLInputElement>('input').value).toBe('');
        window.removeEventListener('tdrive:unlocked', listener);
    });

    it('reports a wrong password and keeps the card where it is', async () => {
        const unlocked = vi.fn();
        api.useEncryptionPassword.mockRejectedValue(new Error('Incorrect password'));
        byId<HTMLInputElement>('input').value = 'nope';

        await card.submit(unlocked);

        expect(unlocked).not.toHaveBeenCalled();
        expect(byId('error').textContent).toContain('Incorrect password');
        expect(byId('error').style.display).toBe('block');
        expect(byId<HTMLInputElement>('input').value).toBe('');
    });

    it('refuses a password the backend rejected instead of reporting it unlocked', async () => {
        const unlocked = vi.fn();
        api.useEncryptionPassword.mockResolvedValue({ ok: false, error: { code: 'operation_failed', message: 'Incorrect password' } });
        byId<HTMLInputElement>('input').value = 'nope';

        await card.submit(unlocked);

        expect(unlocked).not.toHaveBeenCalled();
        expect(byId('error').textContent).toContain('Incorrect password');
        expect(byId('error').style.display).toBe('block');
    });

    it('takes the controls away while the password is in flight and gives them back after', async () => {
        let release: (value: { ok: true }) => void = () => {};
        api.useEncryptionPassword.mockImplementation(() => new Promise<{ ok: true }>(resolve => { release = resolve; }));
        byId<HTMLInputElement>('input').value = 'slow';

        const pending = card.submit(() => {});
        expect(byId<HTMLButtonElement>('unlock').disabled).toBe(true);
        expect(byId<HTMLInputElement>('input').disabled).toBe(true);

        release({ ok: true });
        await pending;
        expect(byId<HTMLButtonElement>('unlock').disabled).toBe(false);
        expect(byId<HTMLInputElement>('input').disabled).toBe(false);
    });

    it('gives the controls back even when the password was refused', async () => {
        api.useEncryptionPassword.mockRejectedValue(new Error('no'));
        byId<HTMLInputElement>('input').value = 'nope';

        await card.submit(() => {});

        expect(byId<HTMLButtonElement>('unlock').disabled).toBe(false);
        expect(byId<HTMLInputElement>('input').disabled).toBe(false);
    });

    it('clears password input immediately and ignores a late result after the next card is shown', async () => {
        const response = deferred<{ ok: true }>();
        api.useEncryptionPassword.mockReturnValue(response.promise);
        byId<HTMLInputElement>('input').value = 'old-password';
        const onUnlocked = vi.fn();
        const pending = card.submit(onUnlocked);
        expect(byId<HTMLInputElement>('input').value).toBe('');
        await card.submit(vi.fn());
        expect(api.useEncryptionPassword).toHaveBeenCalledOnce();
        card.hide();
        card.show('new card');
        byId<HTMLInputElement>('input').value = 'new-password';
        response.resolve({ ok: true });
        await pending;
        expect(onUnlocked).not.toHaveBeenCalled();
        expect(byId<HTMLInputElement>('input').value).toBe('new-password');
        expect(encryption.loadEncryptionStatus).not.toHaveBeenCalled();
    });
});

describe('a preview rendered without the unlock markup', () => {
    it('goes on working rather than throwing at every call', async () => {
        const bare = createUnlockCard({
            card: null, input: null, unlockButton: null, eyeButton: null,
            error: null, hint: null, hintText: null,
        });
        await expect((async () => {
            bare.show('hint');
            bare.hide();
            bare.clearInput();
            bare.toggleReveal();
            await bare.submit(() => {});
            await bare.submitBiometric(() => {});
        })()).resolves.toBeUndefined();
        expect(api.useEncryptionPassword).not.toHaveBeenCalled();
    });
});
