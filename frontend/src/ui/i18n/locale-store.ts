import { derived, writable } from 'svelte/store';
import type { Locale } from './messages';

export type LocalePreference = 'system' | Locale;
export const LOCALE_STORAGE_KEY = 'tdrive.locale.v1';

export function resolveLocale(preference: LocalePreference, systemLanguage?: string): Locale {
    if (preference !== 'system') return preference;
    const normalized = (systemLanguage ?? '').toLowerCase().replace('_', '-');
    if (normalized === 'zh-tw' || normalized === 'zh-hant' || normalized.startsWith('zh-hant-')) return 'zh-TW';
    if (normalized === 'zh' || normalized.startsWith('zh-cn') || normalized.startsWith('zh-sg') ||
        normalized === 'zh-hans' || normalized.startsWith('zh-hans-')) return 'zh-CN';
    return 'en';
}

function readPreference(): LocalePreference {
    try {
        const saved = window.localStorage.getItem(LOCALE_STORAGE_KEY);
        if (saved === 'system' || saved === 'en' || saved === 'zh-CN' || saved === 'zh-TW') return saved;
        return 'system';
    } catch {
        return 'system';
    }
}

function readSystemLanguage(): string {
    return typeof navigator === 'undefined' ? 'en' : navigator.language;
}

export const localePreference = writable<LocalePreference>(readPreference());
export const locale = derived(localePreference, ($preference) => resolveLocale($preference, readSystemLanguage()));

export function setLocalePreference(preference: LocalePreference): void {
    localePreference.set(preference);
    try {
        window.localStorage.setItem(LOCALE_STORAGE_KEY, preference);
    } catch {
        // The in-memory preference still applies when storage is unavailable.
    }
}
