import { describe, expect, it } from 'vitest';
import { normalizePhoneNumber, countryForInternationalNumber, nationalNumberForCountryChange, countries } from './phone-number';

describe('Telegram phone numbers', () => {
    it.each([
        ['98765 43210', 'IN', '+919876543210'],
        ['+44 (0)7700 900123', 'IN', '+447700900123'],
        ['07700 900123', 'GB', '+447700900123'],
        ['02 36618 300', 'IT', '+390236618300'],
        ['(202) 555-0123', 'US', '+12025550123'],
        ['+44 7700 900123', 'IN', '+447700900123'],
        ['+91 98765 43210', 'IN', '+919876543210'],
        ['+888 1234 5678', undefined, '+88812345678'],
    ] as const)('normalizes %s without changing meaningful digits', (input, country, expected) => {
        expect(normalizePhoneNumber(input, country)).toEqual({ number: expected });
    });
    it.each(['', 'hello 1234567890', '+', '+1+2025550123', '123 ext 45'])('rejects malformed input %s', (input) => {
        expect(normalizePhoneNumber(input, 'US')).toHaveProperty('error');
    });
    it('requires a country for national input', () => {
        expect(normalizePhoneNumber('9876543210')).toHaveProperty('error');
    });
    it('does not invent a country for ambiguous or anonymous numbers', () => {
        expect(countryForInternationalNumber('+1')).toBeUndefined();
        expect(countryForInternationalNumber('+88812345678')).toBeUndefined();
        expect(countryForInternationalNumber('+14165550123')).toBe('CA');
    });
    it('includes searchable country names and shared calling codes', () => {
        expect(countries.find(c => c.code === 'IN')).toMatchObject({ name: 'India', callingCode: '91' });
        expect(countries.find(c => c.code === 'CA')).toMatchObject({ callingCode: '1' });
    });
});

it.each([['+91', ''], ['+919', '9'], ['+919876543210', '9876543210'], ['98765', '98765']])('keeps national digits when changing country from %s', (raw, expected) => {
    expect(nationalNumberForCountryChange(raw)).toBe(expected);
});
