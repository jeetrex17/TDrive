import type { OperationError, OperationErrorCode, OperationResult } from "../types";
import { asRecord } from "./shared";

const OPERATION_ERROR_CODES: Record<OperationErrorCode, true> = {
    operation_failed: true,
    backend_unavailable: true,
    encryption_password_required: true,
    invalid_encryption_password: true,
    encryption_password_already_set: true,
    encryption_policy_unavailable: true,
    canceled: true,
    deadline_exceeded: true,
    not_found: true,
    permission_denied: true,
    already_exists: true,
    insufficient_storage: true,
    network_unavailable: true,
    file_too_large: true,
};

export function normalizeOperationResult(value: unknown, fallbackMessage = "Operation failed"): OperationResult {
    const raw = asRecord(value);
    if (raw.ok === true) return { ok: true };

    const rawError = asRecord(raw.error);
    const rawCode = String(rawError.code ?? "");
    const code: OperationErrorCode = Object.prototype.hasOwnProperty.call(OPERATION_ERROR_CODES, rawCode)
        ? rawCode as OperationErrorCode
        : 'operation_failed';
    const message = String(rawError.message ?? fallbackMessage).trim() || fallbackMessage;
    return { ok: false, error: { code, message } };
}

export class OperationFailure extends Error {
    readonly code: OperationErrorCode;

    constructor(error: OperationError) {
        super(error.message);
        this.name = 'OperationFailure';
        this.code = error.code;
    }
}

export function requireOperationSuccess(result: OperationResult): void {
    if (!result.ok) throw new OperationFailure(result.error);
}

export function hasOperationErrorCode(error: unknown, code: OperationErrorCode): error is OperationFailure {
    return error instanceof OperationFailure && error.code === code;
}
