import { afterEach, describe, expect, it, vi } from 'vitest';
import { render } from 'svelte/server';
import AuthScreens from './AuthScreens.svelte';
import { showAuthView, showStartupView } from '../app/app-store';
import {
    authHint,
    authPhone,
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
    vi.unstubAllGlobals();
    showStartupView();
    authPhone.set('');
    authHint.set('');
    resetAuthSubmissions();
});

// The platform helpers read the browser-preview override off the URL; a
// server render has no window, so this is the whole phone.
function previewPhone(): void {
    vi.stubGlobal('window', { location: { search: '?mobile=1' } });
}

describe('AuthScreens', () => {
    it('renders no auth form when no screen is active', () => {
        showStartupView();
        expect(render(AuthScreens, { props }).body).not.toContain('auth-form');
    });

    it('renders setup as a labelled credential form with local-storage guidance', () => {
        showAuthView('setup');
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
        showAuthView('code');
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
        showAuthView('password');
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
        showAuthView('code');
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

    it('keeps the desktop card free of phone-only markup', () => {
        showAuthView('setup');
        const { body } = render(AuthScreens, { props });
        expect(body).not.toContain('auth-welcome');
        expect(body).not.toContain('enterkeyhint');
        expect(body).not.toContain('input-with-action');
        expect(body).not.toContain('auth-inline-link');
        expect(body).toContain('auth-page-body');
        expect(body).toContain('auth-actions');
    });

    it('renders the phone welcome page in front of setup', () => {
        previewPhone();
        showAuthView('setup');
        const { body } = render(AuthScreens, { props });
        expect(body).toContain('auth-welcome');
        expect(body).toContain('Your Telegram, as a drive.');
        expect(body).toContain('Continue');
        expect(body).not.toContain('<form');
    });

    it('renders phone keyboard hints and the external credentials link', () => {
        previewPhone();
        showAuthView('code');
        expect(render(AuthScreens, { props }).body).toContain('enterkeyhint="go"');

        showAuthView('phone');
        expect(render(AuthScreens, { props }).body).toContain('enterkeyhint="send"');
    });
});
