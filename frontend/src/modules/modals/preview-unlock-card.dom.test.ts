import { beforeEach, describe, expect, it, vi } from 'vitest';

const api = vi.hoisted(() => ({ useEncryptionPassword: vi.fn(async () => {}) }));
const encryption = vi.hoisted(() => ({ loadEncryptionStatus: vi.fn(async () => {}) }));

vi.mock('../../api', () => ({ useEncryptionPassword: api.useEncryptionPassword }));
vi.mock('../encryption', () => ({ loadEncryptionStatus: encryption.loadEncryptionStatus }));

import { createUnlockCard, type UnlockCard } from './preview-unlock-card';

const MARKUP = `
    <div id="card" style="display: none;">
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
    });
}

beforeEach(() => {
    vi.clearAllMocks();
    api.useEncryptionPassword.mockResolvedValue(undefined);
    document.body.innerHTML = MARKUP;
    card = build();
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
    });

    it('takes the controls away while the password is in flight and gives them back after', async () => {
        let release = () => {};
        api.useEncryptionPassword.mockImplementation(() => new Promise<void>(resolve => { release = resolve; }));
        byId<HTMLInputElement>('input').value = 'slow';

        const pending = card.submit(() => {});
        expect(byId<HTMLButtonElement>('unlock').disabled).toBe(true);
        expect(byId<HTMLInputElement>('input').disabled).toBe(true);

        release();
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
        })()).resolves.toBeUndefined();
        expect(api.useEncryptionPassword).not.toHaveBeenCalled();
    });
});
