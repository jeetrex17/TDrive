import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { LOCALE_STORAGE_KEY, setLocalePreference } from './locale-store';

describe('locale preference storage', () => {
    beforeEach(() => {
        window.localStorage.clear();
    });

    afterEach(() => {
        window.localStorage.clear();
    });

    it('persists a selected language', () => {
        setLocalePreference('zh-CN');
        expect(window.localStorage.getItem(LOCALE_STORAGE_KEY)).toBe('zh-CN');
    });

    it('persists the system default preference', () => {
        setLocalePreference('system');
        expect(window.localStorage.getItem(LOCALE_STORAGE_KEY)).toBe('system');
    });
});
