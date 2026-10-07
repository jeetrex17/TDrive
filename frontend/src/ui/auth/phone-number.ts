import { AsYouType, getCountries, getCountryCallingCode, parsePhoneNumberFromString, type CountryCode } from 'libphonenumber-js/min';

const names = new Intl.DisplayNames(['en'], { type: 'region' });
export const countries = getCountries().map(code => ({
    code,
    name: names.of(code) ?? code,
    callingCode: getCountryCallingCode(code),
    flag: [...code].map(letter => String.fromCodePoint(127397 + letter.charCodeAt(0))).join(''),
})).sort((a, b) => a.name.localeCompare(b.name, 'en'));

export function countryForInternationalNumber(raw: string): CountryCode | undefined {
    if (!raw.trim().startsWith('+')) return undefined;
    return parsePhoneNumberFromString(raw, { extract: false })?.country;
}

export function nationalNumberForCountryChange(raw: string): string {
    if (!raw.trim().startsWith('+')) return raw;
    const parser = new AsYouType();
    parser.input(raw);
    const callingCode = parser.getCallingCode();
    return callingCode ? parser.getChars().slice(1 + callingCode.length) : '';
}

// Telegram remains authoritative about account eligibility, including Fragment numbers.
// Use metadata to handle national prefixes, with a syntax-only fallback for
// international numbers absent from current allocation metadata.
export function normalizePhoneNumber(raw: string, country?: CountryCode): { number: string } | { error: string } {
    const input = raw.trim();
    if (!input) return { error: 'Enter the phone number for your Telegram account.' };
    if (!/^\+?[\d\s().-]+$/.test(input)) {
        return { error: 'Enter a phone number using digits and an optional leading +.' };
    }
    const digits = input.replace(/\D/g, '');
    if (input.startsWith('+')) {
        if (!/^[1-9]\d{3,14}$/.test(digits)) return { error: 'Check your country code and phone number.' };
        const parsed = parsePhoneNumberFromString(input, { extract: false });
        return { number: parsed?.countryCallingCode !== '888' && parsed?.isPossible()
            ? parsed.number : `+${digits}` };
    }
    if (!country) return { error: 'Choose a country or enter your full number starting with +.' };
    const parsed = parsePhoneNumberFromString(input, { defaultCountry: country, extract: false });
    if (!parsed || !parsed.isPossible()) return { error: 'Check the phone number for the selected country.' };
    return { number: parsed.number };
}
