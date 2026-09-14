import { afterEach, describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import AuthScreens from './AuthScreens.svelte';
import {
    authHint,
    authPhone,
    authScreen,
    beginAuthSubmission,
    failAuthSubmission,
    resetAuthSubmissions,
} from './auth-store';

const noop = () => {};
const props = {
    onSetup: noop,
    onPhone: noop,
    onCode: noop,
    onPassword: noop,
    onBackToPhone: noop,
};

afterEach(() => {
    authScreen.set(null);
    authPhone.set('');
    authHint.set('');
    resetAuthSubmissions();
});

describe('AuthScreens', () => {
    it('renders no auth form when no screen is active', () => {
        authScreen.set(null);
        expect(render(AuthScreens, { props }).body).not.toContain('auth-form');
    });

    it('renders setup as a labelled credential form with local-storage guidance', () => {
        authScreen.set('setup');
        const { body } = render(AuthScreens, { props });

        expect(body).toContain('<form');
        expect(body).toContain('aria-labelledby="auth-setup-title"');
        expect(body).toContain('for="telegram-api-id"');
        expect(body).toContain('API ID');
        expect(body).toContain('name="api-id"');
        expect(body).toContain('inputmode="numeric"');
        expect(body).toContain('for="telegram-api-hash"');
        expect(body).toContain('API hash');
        expect(body).toContain('name="api-hash"');
        expect(body).toContain('type="password"');
        expect(body).toContain('my.telegram.org/apps');
        expect(body).toContain("private app-data folder on this device");
        expect(body).toContain('no analytics or external tracking');
        expect(body).toContain('type="submit"');
    });


    it('shows the destination and one-time-code semantics on the code step', () => {
        authScreen.set('code');
        expect(render(AuthScreens, { props }).body).not.toContain('auth-helper-pill');

        authPhone.set('+15551234567');
        const { body } = render(AuthScreens, { props });
        expect(body).toContain('auth-helper-pill');
        expect(body).toContain('+15551234567');
        expect(body).toContain('for="telegram-code"');
        expect(body).toContain('Login code');
        expect(body).toContain('name="login-code"');
        expect(body).toContain('autocomplete="one-time-code"');
        expect(body).toContain('inputmode="numeric"');
        expect(body).toContain('Verify');
    });

    it('associates the escaped 2FA hint with the password field', () => {
        authScreen.set('password');
        authHint.set('<img src=x>');
        const { body } = render(AuthScreens, { props });

        expect(body).toContain('for="telegram-password"');
        expect(body).toContain('Telegram password');
        expect(body).toContain('name="password"');
        expect(body).toContain('autocomplete="current-password"');
        expect(body).toContain('aria-describedby="password-hint"');
        expect(body).not.toContain('<img src=x>');
    });

    it('renders each flow error inline and exposes its busy state', () => {
        authScreen.set('code');
        beginAuthSubmission('code');
        let body = render(AuthScreens, { props }).body;
        expect(body).toContain('aria-busy="true"');
        expect(body).toContain('disabled');
        expect(body).toContain('Verifying…');

        failAuthSubmission('code', 'That code was incorrect.');
        body = render(AuthScreens, { props }).body;
        expect(body).toContain('id="code-error"');
        expect(body).toContain('aria-live="polite"');
        expect(body).toContain('aria-invalid="true"');
        expect(body).toContain('That code was incorrect.');
    });
});
