import type { OperationErrorCode } from '../types';

const MAX_USER_MESSAGE_LENGTH = 240;
const MAX_DIAGNOSTIC_MESSAGE_LENGTH = 480;
const MAX_DIAGNOSTIC_STACK_LENGTH = 1_600;
const MAX_CAUSE_DEPTH = 5;

const LOCAL_PATH = /(?:file:\/\/)?\/(?:Users|home|tmp|private|var\/folders)\/[^\s',;)]+/gi;
const WINDOWS_PATH = /\b[A-Z]:\\(?:[^\\\r\n]+\\)*[^\s,;)'"]+/gi;
const HOME_PATH = /(^|\s)~\/(?:[^\s',;)]+)/g;
const SECRET_ASSIGNMENT = /\b(api[_ -]?hash|access[_ -]?token|refresh[_ -]?token|token|password|secret|authorization)\b\s*[:=]\s*(?:"[^"]*"|'[^']*'|[^\s,;]+)/gi;
const URL_SECRET = /([?&](?:api[_-]?hash|access[_-]?token|refresh[_-]?token|token|password|secret)=)[^&#\s]+/gi;
const URL_CREDENTIALS = /(https?:\/\/)[^\s/@:]+:[^\s/@]+@/gi;
const JWT = /\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b/g;
const PRIVATE_KEY = /-----BEGIN [^-\r\n]*PRIVATE KEY-----[\s\S]*?-----END [^-\r\n]*PRIVATE KEY-----/gi;
const SAFE_CODE = /^[a-z][a-z0-9_]{0,63}$/;

export type AppErrorKind =
    | 'authentication'
    | 'permission'
    | 'not_found'
    | 'conflict'
    | 'validation'
    | 'network'
    | 'timeout'
    | 'storage'
    | 'canceled'
    | 'unavailable'
    | 'render'
    | 'unexpected';

export type AppErrorSource = 'application' | 'backend' | 'runtime' | 'startup' | 'render';

export interface SafeErrorDetails {
    readonly name: string;
    readonly message?: string;
    readonly code?: string;
    readonly stack?: string;
}

type AppErrorVariant<Kind extends AppErrorKind> = Readonly<{
    kind: Kind;
    title: string;
    message: string;
    retryable: boolean;
    source: AppErrorSource;
    code?: OperationErrorCode;
    details: SafeErrorDetails;
    /** The untouched failure is retained for control flow, never for presentation. */
    cause: unknown;
}>;

/** A UI-safe failure whose kind can be exhaustively narrowed by consumers. */
export type AppError = {
    [Kind in AppErrorKind]: AppErrorVariant<Kind>;
}[AppErrorKind];

export interface AppErrorOptions {
    source?: AppErrorSource;
}

interface ErrorPresentation {
    kind: AppErrorKind;
    title: string;
    message: string;
    retryable: boolean;
}

const OPERATION_PRESENTATIONS: Record<OperationErrorCode, ErrorPresentation> = {
    operation_failed: {
        kind: 'unexpected',
        title: 'Action failed',
        message: '',
        retryable: true,
    },
    backend_unavailable: {
        kind: 'unavailable',
        title: 'TDrive is not ready',
        message: 'TDrive is not ready yet. Try again.',
        retryable: true,
    },
    encryption_password_required: {
        kind: 'authentication',
        title: 'Password required',
        message: 'Enter your encryption password first.',
        retryable: false,
    },
    invalid_encryption_password: {
        kind: 'authentication',
        title: 'Password not accepted',
        message: 'That encryption password is incorrect.',
        retryable: false,
    },
    encryption_password_already_set: {
        kind: 'conflict',
        title: 'Encryption already configured',
        message: 'Encryption is already configured.',
        retryable: false,
    },
    encryption_policy_unavailable: {
        kind: 'unavailable',
        title: 'Encryption unavailable',
        message: 'Encryption is unavailable for this drive.',
        retryable: false,
    },
    canceled: {
        kind: 'canceled',
        title: 'Canceled',
        message: 'Canceled.',
        retryable: false,
    },
    deadline_exceeded: {
        kind: 'timeout',
        title: 'Request timed out',
        message: 'The request took too long. Try again.',
        retryable: true,
    },
    not_found: {
        kind: 'not_found',
        title: 'Item not found',
        message: 'That item no longer exists. Refresh and try again.',
        retryable: true,
    },
    permission_denied: {
        kind: 'permission',
        title: 'Permission denied',
        message: "You don't have permission to do that.",
        retryable: false,
    },
    already_exists: {
        kind: 'conflict',
        title: 'Item already exists',
        message: 'This item is already there.',
        retryable: false,
    },
    insufficient_storage: {
        kind: 'storage',
        title: 'Not enough storage',
        message: 'There is not enough free disk space to finish this action.',
        retryable: false,
    },
    network_unavailable: {
        kind: 'network',
        title: 'Telegram is unavailable',
        message: 'Telegram is not reachable right now. Try again.',
        retryable: true,
    },
    file_too_large: {
        kind: 'validation',
        title: 'File is too large',
        message: 'This file is too large to upload.',
        retryable: false,
    },
};

const APP_ERROR_KINDS: Record<AppErrorKind, true> = {
    authentication: true,
    permission: true,
    not_found: true,
    conflict: true,
    validation: true,
    network: true,
    timeout: true,
    storage: true,
    canceled: true,
    unavailable: true,
    render: true,
    unexpected: true,
};

const APP_ERROR_SOURCES: Record<AppErrorSource, true> = {
    application: true,
    backend: true,
    runtime: true,
    startup: true,
    render: true,
};

function property(value: unknown, key: string): unknown {
    if ((typeof value !== 'object' || value === null) && typeof value !== 'function') return undefined;
    try {
        return Reflect.get(value, key);
    } catch {
        return undefined;
    }
}

function textProperty(value: unknown, key: string): string | undefined {
    const candidate = property(value, key);
    return typeof candidate === 'string' ? candidate : undefined;
}

function redact(text: string): string {
    return text
        .replace(PRIVATE_KEY, '[private key redacted]')
        .replace(URL_CREDENTIALS, '$1[credentials redacted]@')
        .replace(URL_SECRET, '$1[redacted]')
        .replace(SECRET_ASSIGNMENT, (assignment, key: string) => {
            const separator = assignment.includes(':') ? ':' : '=';
            return key + separator + '[redacted]';
        })
        .replace(JWT, '[token redacted]')
        .replace(LOCAL_PATH, '[local path]')
        .replace(WINDOWS_PATH, '[local path]')
        .replace(HOME_PATH, '$1[local path]');
}

function singleLine(text: string, maxLength: number): string {
    return redact(text)
        .replace(/^(?:Error:?\s*)+/i, '')
        .replace(/\s+/g, ' ')
        .trim()
        .slice(0, maxLength);
}

function diagnosticText(text: string, maxLength: number): string {
    return redact(text)
        .replace(/\r\n?/g, '\n')
        .split('\n')
        .map((line) => line.replace(/[\t ]+/g, ' ').trimEnd())
        .join('\n')
        .trim()
        .slice(0, maxLength);
}

function causeChain(error: unknown): unknown[] {
    const chain: unknown[] = [];
    const seen = new Set<object>();
    let current: unknown = error;

    for (let depth = 0; depth < MAX_CAUSE_DEPTH && current !== undefined && current !== null; depth += 1) {
        if ((typeof current === 'object' && current !== null) || typeof current === 'function') {
            if (seen.has(current)) break;
            seen.add(current);
        }
        chain.push(current);
        current = property(current, 'cause');
    }

    return chain;
}

function rawMessage(chain: readonly unknown[]): string {
    for (const item of chain) {
        if (typeof item === 'string') return item;
        const message = textProperty(item, 'message');
        if (message) return message;
    }
    return '';
}

function knownOperationCode(chain: readonly unknown[]): OperationErrorCode | undefined {
    for (const item of chain) {
        const code = textProperty(item, 'code');
        if (code && Object.prototype.hasOwnProperty.call(OPERATION_PRESENTATIONS, code)) {
            return code as OperationErrorCode;
        }
    }
    return undefined;
}

function diagnosticCode(chain: readonly unknown[]): string | undefined {
    for (const item of chain) {
        const code = textProperty(item, 'code');
        if (code && SAFE_CODE.test(code)) return code;
    }
    return undefined;
}

function safeDetails(chain: readonly unknown[], raw: string): SafeErrorDetails {
    const first = chain[0];
    const rawName = textProperty(first, 'name');
    const name = singleLine(rawName || (typeof first === 'string' ? 'Error' : 'UnknownError'), 80)
        || 'UnknownError';
    const message = diagnosticText(raw, MAX_DIAGNOSTIC_MESSAGE_LENGTH);
    const rawStack = chain.map((item) => textProperty(item, 'stack')).find(Boolean);
    const stack = rawStack ? diagnosticText(rawStack, MAX_DIAGNOSTIC_STACK_LENGTH) : '';
    const code = diagnosticCode(chain);

    return Object.freeze({
        name,
        ...(message ? { message } : {}),
        ...(code ? { code } : {}),
        ...(stack ? { stack } : {}),
    });
}

function classifyMessage(raw: string, source: AppErrorSource): ErrorPresentation {
    const message = singleLine(raw, MAX_USER_MESSAGE_LENGTH);
    const lower = message.toLowerCase();

    if (source === 'render') {
        return {
            kind: 'render',
            title: 'This screen needs a reset',
            message: "TDrive couldn't finish drawing this screen. Your stored files are safe.",
            retryable: true,
        };
    }
    if (lower.includes('move would create cycle') || lower.includes('own subfolder')) {
        return {
            kind: 'validation',
            title: 'Folder cannot be moved there',
            message: "Can't move a folder into itself or one of its subfolders.",
            retryable: false,
        };
    }
    if (lower.includes('only the uploader can')) {
        return { kind: 'permission', title: 'Uploader permission required', message, retryable: false };
    }
    if (lower.includes('file is already in this folder') || lower.includes('folder is already here')) {
        return OPERATION_PRESENTATIONS.already_exists;
    }
    if (lower.includes('invalid target') || lower.includes('target folder not found')) {
        return {
            kind: 'validation',
            title: 'Destination unavailable',
            message: 'Choose a valid destination folder.',
            retryable: false,
        };
    }
    if (lower.includes('not found')) return OPERATION_PRESENTATIONS.not_found;
    if (lower.includes('encryption password required')) {
        return OPERATION_PRESENTATIONS.encryption_password_required;
    }
    if (lower.includes('phone_number_invalid') || lower.includes('invalid phone')) {
        return {
            kind: 'validation',
            title: 'Check the phone number',
            message: 'Enter a valid phone number, including the country code.',
            retryable: false,
        };
    }
    if (lower.includes('phone_code_invalid') || lower.includes('invalid code')) {
        return {
            kind: 'authentication',
            title: 'Code not accepted',
            message: 'That code was incorrect. Check it and try again.',
            retryable: false,
        };
    }
    if (lower.includes('password_hash_invalid') || lower.includes('invalid password')) {
        return {
            kind: 'authentication',
            title: 'Password not accepted',
            message: 'That two-step verification password was incorrect.',
            retryable: false,
        };
    }
    if (lower.includes('flood_wait') || lower.includes('too many requests')) {
        return {
            kind: 'network',
            title: 'Try again shortly',
            message: 'Telegram is temporarily limiting attempts. Wait a moment and try again.',
            retryable: true,
        };
    }
    if (lower.includes('auth_key_unregistered') || lower.includes('session expired')) {
        return {
            kind: 'authentication',
            title: 'Sign in again',
            message: 'Your Telegram session expired. Sign in again.',
            retryable: false,
        };
    }
    if (lower.includes('permission denied') || lower.includes('not authorized')) {
        return OPERATION_PRESENTATIONS.permission_denied;
    }
    if (lower.includes('no space left') || lower.includes('disk full')) {
        return OPERATION_PRESENTATIONS.insufficient_storage;
    }
    if (lower.includes('deadline exceeded') || lower.includes('timed out') || lower.includes('timeout')) {
        return OPERATION_PRESENTATIONS.deadline_exceeded;
    }
    if (lower.includes('tg client') || lower.includes('telegram') || lower.includes('network')) {
        return OPERATION_PRESENTATIONS.network_unavailable;
    }
    if (source === 'startup') {
        return {
            kind: 'unavailable',
            title: 'TDrive could not start',
            message: 'TDrive could not finish starting. Reload the app and try again.',
            retryable: true,
        };
    }
    if (source === 'runtime') {
        return {
            kind: 'unavailable',
            title: 'Desktop services unavailable',
            message: 'TDrive lost access to its desktop services. Reload the app and try again.',
            retryable: true,
        };
    }
    return {
        kind: 'unexpected',
        title: 'Something went wrong',
        message: message || 'Something went wrong. Try again.',
        retryable: true,
    };
}

export function isAppError(value: unknown): value is AppError {
    const kind = property(value, 'kind');
    const source = property(value, 'source');
    return typeof kind === 'string'
        && Object.prototype.hasOwnProperty.call(APP_ERROR_KINDS, kind)
        && typeof source === 'string'
        && Object.prototype.hasOwnProperty.call(APP_ERROR_SOURCES, source)
        && typeof property(value, 'title') === 'string'
        && typeof property(value, 'message') === 'string'
        && typeof property(value, 'retryable') === 'boolean';
}

/** Normalize any thrown value without coercing arbitrary objects into user copy. */
export function toAppError(error: unknown, options: AppErrorOptions = {}): AppError {
    const source = options.source ?? 'application';
    if (isAppError(error) && error.source === source) return error;

    const chain = causeChain(error);
    const raw = rawMessage(chain);
    const code = knownOperationCode(chain);
    let presentation = classifyMessage(raw, source);

    if (source !== 'render' && code) {
        const operationPresentation = OPERATION_PRESENTATIONS[code];
        presentation = operationPresentation.message
            ? operationPresentation
            : {
                ...operationPresentation,
                message: presentation.message,
                kind: presentation.kind,
                title: presentation.title,
                retryable: presentation.retryable,
            };
    }

    return Object.freeze({
        ...presentation,
        source,
        ...(code ? { code } : {}),
        details: safeDetails(chain, raw),
        cause: error,
    }) as AppError;
}

/** Converts backend failures into concise, non-sensitive copy safe for inline UI. */
export function humanizeBackendError(error: unknown): string {
    return toAppError(error, { source: 'backend' }).message;
}

/** Formats only already-redacted fields; the original cause is deliberately excluded. */
export function formatAppErrorDiagnostic(error: AppError): string {
    const lines = [
        'Source: ' + error.source,
        'Kind: ' + error.kind,
        ...(error.details.code ? ['Code: ' + error.details.code] : []),
        'Type: ' + error.details.name,
        ...(error.details.message ? ['Message: ' + error.details.message] : []),
        ...(error.details.stack ? ['Stack:\n' + error.details.stack] : []),
    ];
    return lines.join('\n');
}
