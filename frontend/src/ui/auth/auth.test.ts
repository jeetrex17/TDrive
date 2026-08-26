import { afterEach, describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import AuthScreens from './AuthScreens.svelte';
import { authHint, authPhone, authScreen } from './auth-store';

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
});

describe('AuthScreens', () => {
    it('renders no auth box when no screen is active', () => {
        authScreen.set(null);
        expect(render(AuthScreens, { props }).body).not.toContain('auth-box');
    });

    it('renders the setup screen', () => {
        authScreen.set('setup');
        const { body } = render(AuthScreens, { props });
        expect(body).toContain('System Setup');
        expect(body).toContain('Save Configuration');
    });

    it('renders the phone screen', () => {
        authScreen.set('phone');
        const { body } = render(AuthScreens, { props });
        expect(body).toContain('Welcome Back');
        expect(body).toContain('Send Code');
        expect(body).toContain('id="auth-appearance-trigger"');
        expect(body).toContain('Customize appearance');
    });

    it('shows the sent-to pill on the code screen only when a phone is set', () => {
        authScreen.set('code');
        expect(render(AuthScreens, { props }).body).not.toContain('auth-helper-pill');

        authPhone.set('+15551234567');
        const { body } = render(AuthScreens, { props });
        expect(body).toContain('auth-helper-pill');
        expect(body).toContain('+15551234567');
        expect(body).toContain('Verify');
    });

    it('shows the 2FA hint row only when a hint is set', () => {
        authScreen.set('password');
        expect(render(AuthScreens, { props }).body).not.toContain('auth-caption');

        authHint.set('rhymes with cat');
        const { body } = render(AuthScreens, { props });
        expect(body).toContain('auth-caption');
        expect(body).toContain('rhymes with cat');
        expect(body).toContain('Unlock');
    });

    it('escapes an untrusted hint', () => {
        authScreen.set('password');
        authHint.set('<img src=x>');
        expect(render(AuthScreens, { props }).body).not.toContain('<img src=x>');
    });
});
