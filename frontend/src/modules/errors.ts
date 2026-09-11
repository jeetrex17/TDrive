const LOCAL_PATH = new RegExp("(?:file://)?/(?:Users|home|tmp|private|var/folders)/[^\\s',;)]+", "gi");
const SECRET_ASSIGNMENT = /\b(api[_ -]?hash|access[_ -]?token|token|password|secret)\b\s*[:=]\s*[^\s,;]+/gi;
const MAX_USER_MESSAGE_LENGTH = 240;

function cleanBackendMessage(error: unknown): string {
    const message = (error as { message?: unknown } | null | undefined)?.message;
    return String(message ?? error ?? '')
        .replace(/^Error:?\s*/i, '')
        .replace(LOCAL_PATH, '[local path]')
        .replace(SECRET_ASSIGNMENT, '$1=[redacted]')
        .replace(/\s+/g, ' ')
        .trim()
        .slice(0, MAX_USER_MESSAGE_LENGTH);
}

/** Converts backend failures into concise, non-sensitive copy safe for UI surfaces. */
export function humanizeBackendError(error: unknown): string {
    const raw = cleanBackendMessage(error);
    const lower = raw.toLowerCase();

    if (!raw) return 'Something went wrong. Try again.';
    if (lower.includes('move would create cycle') || lower.includes('own subfolder')) {
        return "Can't move a folder into itself or one of its subfolders.";
    }
    if (lower.includes('only the uploader can')) return raw;
    if (lower.includes('file is already in this folder') || lower.includes('folder is already here')) {
        return 'This item is already there.';
    }
    if (lower.includes('invalid target') || lower.includes('target folder not found')) {
        return 'Choose a valid destination folder.';
    }
    if (lower.includes('not found')) {
        return 'That item no longer exists. Refresh and try again.';
    }
    if (lower.includes('encryption password required')) {
        return 'Enter your encryption password first.';
    }
    if (lower.includes('phone_number_invalid') || lower.includes('invalid phone')) {
        return 'Enter a valid phone number, including the country code.';
    }
    if (lower.includes('phone_code_invalid') || lower.includes('invalid code')) {
        return 'That code was incorrect. Check it and try again.';
    }
    if (lower.includes('password_hash_invalid') || lower.includes('invalid password')) {
        return 'That two-step verification password was incorrect.';
    }
    if (lower.includes('flood_wait') || lower.includes('too many requests')) {
        return 'Telegram is temporarily limiting attempts. Wait a moment and try again.';
    }
    if (lower.includes('auth_key_unregistered') || lower.includes('session expired')) {
        return 'Your Telegram session expired. Sign in again.';
    }
    if (lower.includes('permission denied') || lower.includes('not authorized')) {
        return "You don't have permission to do that.";
    }
    if (lower.includes('no space left') || lower.includes('disk full')) {
        return 'There is not enough free disk space to finish this action.';
    }
    if (lower.includes('deadline exceeded') || lower.includes('timed out') || lower.includes('timeout')) {
        return 'The request took too long. Try again.';
    }
    if (lower.includes('tg client') || lower.includes('telegram') || lower.includes('network')) {
        return 'Telegram is not reachable right now. Try again.';
    }
    return raw;
}
