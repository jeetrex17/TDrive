import { afterEach, describe, expect, it } from 'vitest';
import { get } from 'svelte/store';
import {
    appView,
    authScreen,
    showAuthView,
    showDashboardView,
    showLoadingView,
    showStartupView,
} from './app-store';

afterEach(() => {
    showStartupView();
});

describe('application view state', () => {
    it('keeps the session view and active auth screen in one atomic state', () => {
        showLoadingView('Checking your session…');
        expect(get(appView)).toEqual({ kind: 'loading', message: 'Checking your session…' });
        expect(get(authScreen)).toBeNull();

        showAuthView('code');
        expect(get(appView)).toEqual({ kind: 'auth', screen: 'code' });
        expect(get(authScreen)).toBe('code');

        showDashboardView();
        expect(get(appView)).toEqual({ kind: 'dashboard' });
        expect(get(authScreen)).toBeNull();
    });
});
