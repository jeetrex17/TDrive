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

    it('keeps raw backend details as the failure cause', () => {
        const rawError = {
            code: 'permission_denied',
            message: 'Access is denied',
            details: { requestId: 'req-7' },
        };
        const result = normalizeOperationResult({ ok: false, error: rawError });
        let failure: unknown;

        try {
            requireOperationSuccess(result);
        } catch (error) {
            failure = error;
        }

        expect(failure).toBeInstanceOf(OperationFailure);
        if (failure instanceof OperationFailure) {
            expect(failure.cause).toBe(rawError);
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

    it('does not coerce arbitrary backend error values', () => {
            const uncoercible = {
                toString() {
                    throw new Error('backend value must stay opaque');
                },
            };

            expect(normalizeOperationResult({
                ok: false,
                error: { code: uncoercible, message: uncoercible },
            }, 'Safe fallback')).toEqual({
                ok: false,
                error: { code: 'operation_failed', message: 'Safe fallback' },
            });
        });

        it('accepts successful operations without manufacturing an error', () => { const result = normalizeOperationResult({ ok: true });
        expect(() => requireOperationSuccess(result)).not.toThrow();
        expect(result).toEqual({ ok: true }); });
});
