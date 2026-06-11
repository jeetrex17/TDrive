export function humanizeBackendError(err: unknown): string {
    const message = (err as { message?: unknown } | null | undefined)?.message;
    const raw = String(message ?? err ?? '').replace(/^Error:?\s*/i, '').trim();
    const lower = raw.toLowerCase();

    if (!raw) return 'Something went wrong.';
    if (lower.includes('move would create cycle') || lower.includes('own subfolder')) {
        return "Can't move a folder into itself or one of its subfolders.";
    }
    if (lower.includes('only the uploader can')) {
        return raw;
    }
    if (lower.includes('file is already in this folder') || lower.includes('folder is already here')) {
        return 'This item is already there.';
    }
    if (lower.includes('not found')) {
        return 'That item no longer exists. Refresh and try again.';
    }
    if (lower.includes('invalid target') || lower.includes('target folder not found')) {
        return 'Choose a valid destination folder.';
    }
    if (lower.includes('tg client') || lower.includes('telegram') || lower.includes('network')) {
        return 'Telegram is not reachable right now. Try again.';
    }
    if (lower.includes('encryption password required')) {
        return 'Enter your encryption password first.';
    }
    return raw;
}
