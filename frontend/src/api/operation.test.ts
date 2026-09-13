import { describe, expect, it } from 'vitest';
import {
    hasOperationErrorCode,
    normalizeOperationResult,
    OperationFailure,
    requireOperationSuccess,
} from './operation';

describe('operation result contract', () => {
    it('preserves stable error codes independently of display messages', () => {
        const result = normalizeOperationResult({
            ok: false,
            error: {
                code: 'encryption_password_required',
                message: 'Localized text that callers must not parse',
            },
        });

        expect(result).toEqual({
            ok: false,
            error: {
                code: 'encryption_password_required',
                message: 'Localized text that callers must not parse',
            },
        });
        expect(() => requireOperationSuccess(result)).toThrow(OperationFailure);

        try {
            requireOperationSuccess(result);
        } catch (error) {
            expect(hasOperationErrorCode(error, 'encryption_password_required')).toBe(true);
            expect(hasOperationErrorCode(error, 'invalid_encryption_password')).toBe(false);
        }
    });

    it('normalizes unknown backend codes without exposing them to control flow', () => {
        expect(normalizeOperationResult({
            ok: false,
            error: { code: 'future_backend_code', message: '' },
        }, 'Operation failed safely')).toEqual({
            ok: false,
            error: { code: 'operation_failed', message: 'Operation failed safely' },
        });
    });

    it('accepts successful operations without manufacturing an error', () => {
        const result = normalizeOperationResult({ ok: true });
        expect(() => requireOperationSuccess(result)).not.toThrow();
        expect(result).toEqual({ ok: true });
    });
});
