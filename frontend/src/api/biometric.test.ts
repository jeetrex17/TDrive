import { describe, expect, it } from 'vitest';
import { normalizeBiometricStatus } from './biometric';
import { normalizeOperationResult } from './operation';

describe('biometric API contract', () => {
    it('fails closed for malformed availability and never coerces native diagnostic details', () => {
        expect(normalizeBiometricStatus({ available: 'true', enrolled: 1, kind: {}, reason: {} }))
            .toEqual({ available: false, enrolled: false, kind: 'biometric', reason: 'unavailable' });
        expect(normalizeBiometricStatus(null))
            .toEqual({ available: false, enrolled: false, kind: 'biometric', reason: 'unavailable' });
    });
    it('preserves removable enrollment when hardware is unavailable', () => {
        expect(normalizeBiometricStatus({ available: false, enrolled: true, kind: 'touch_id', reason: 'locked_out' }))
            .toEqual({ available: false, enrolled: true, kind: 'touch_id', reason: 'locked_out' });
    });
    it.each(['touch_id', 'face_id', 'windows_hello', 'biometric'])('accepts the native %s label', (kind) => {
        expect(normalizeBiometricStatus({ available: true, enrolled: true, kind, reason: '' }))
            .toEqual({ available: true, enrolled: true, kind, reason: '' });
    });
    it('does not render unknown native reason or type strings', () => {
        expect(normalizeBiometricStatus({ available: false, enrolled: true, kind: 'untrusted label', reason: '/Users/private/Keychain' }))
            .toEqual({ available: false, enrolled: true, kind: 'biometric', reason: 'unavailable' });
    });
    it.each(['biometric_canceled', 'biometric_invalidated', 'biometric_unavailable', 'biometric_not_enrolled', 'biometric_busy', 'biometric_failed', 'biometric_locked_out', 'biometric_cleanup_required'])('preserves stable %s errors for recovery decisions', (code) => {
        expect(normalizeOperationResult({ ok: false, error: { code, message: 'Native detail' } }))
            .toEqual({ ok: false, error: { code, message: 'Native detail' } });
    });
});
