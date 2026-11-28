import { beforeEach, describe, expect, it, vi } from 'vitest';
import { resolveLocale, setLocalePreference, LOCALE_STORAGE_KEY } from './locale-store';

describe('locale selection', () => {
    beforeEach(() => {
        window.localStorage.clear();
    });

    it('resolves Traditional Chinese system locales', () => {
        expect(resolveLocale('system', 'zh-TW')).toBe('zh-TW');
        expect(resolveLocale('system', 'zh-Hant-HK')).toBe('zh-TW');
    });

    it('resolves Simplified Chinese system locales', () => {
        expect(resolveLocale('system', 'zh-CN')).toBe('zh-CN');
        expect(resolveLocale('system', 'zh-SG')).toBe('zh-CN');
        expect(resolveLocale('system', 'zh-Hans')).toBe('zh-CN');
    });

    it('falls back to English for other system locales', () => {
        expect(resolveLocale('system', 'en-US')).toBe('en');
        expect(resolveLocale('system', undefined)).toBe('en');
    });

    it('honours an explicit language preference', () => {
        expect(resolveLocale('zh-TW', 'zh-CN')).toBe('zh-TW');
        expect(resolveLocale('zh-CN', 'en-US')).toBe('zh-CN');
    });

    it('persists a selected language', () => {
        setLocalePreference('zh-CN');
        expect(window.localStorage.getItem(LOCALE_STORAGE_KEY)).toBe('zh-CN');
    });
});
