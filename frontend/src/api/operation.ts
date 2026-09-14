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

const operationErrorCauses = new WeakMap<OperationError, unknown>();

export function normalizeOperationResult(value: unknown, fallbackMessage = "Operation failed"): OperationResult {
    const raw = asRecord(value);
    if (raw.ok === true) return { ok: true };

    const originalError = raw.error;
    const rawError = asRecord(originalError);
    const error: OperationError = {
        code: operationCode(rawError.code),
        message: operationMessage(rawError.message, fallbackMessage),
    };
    operationErrorCauses.set(error, originalError);
    return { ok: false, error };
}

export class OperationFailure extends Error {
    readonly code: OperationErrorCode;
    readonly cause: unknown;

    constructor(error: OperationError) {
        super(error.message);
        this.name = "OperationFailure";
        this.code = error.code;
        this.cause = operationErrorCauses.get(error) ?? error;
    }
}

export function requireOperationSuccess(result: OperationResult): void {
    if (!result.ok) throw new OperationFailure(result.error);
}

export function hasOperationErrorCode(error: unknown, code: OperationErrorCode): error is OperationFailure {
    return error instanceof OperationFailure && error.code === code;
}

function operationCode(value: unknown): OperationErrorCode {
    return typeof value === "string" && Object.prototype.hasOwnProperty.call(OPERATION_ERROR_CODES, value)
        ? value as OperationErrorCode
        : "operation_failed";
}

function operationMessage(value: unknown, fallbackMessage: string): string {
    if (typeof value !== "string") return fallbackMessage;
    return value.trim() || fallbackMessage;
}
