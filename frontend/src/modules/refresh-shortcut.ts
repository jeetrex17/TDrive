type Refresh = () => void | Promise<void>;

let installed = false;

function hasOpenModal(): boolean {
    const overlays = document.querySelectorAll<HTMLElement>('.modal-overlay');
    for (const overlay of overlays) {
        if (overlay.style.display !== 'none') return true;
    }

    return Array.from(document.querySelectorAll<HTMLElement>('[role="dialog"][aria-modal="true"]'))
        .some((dialog) => !dialog.closest('.modal-overlay'));
}

function isRefreshShortcut(event: KeyboardEvent): boolean {
    return (event.metaKey || event.ctrlKey)
        && !event.altKey
        && event.key.toLowerCase() === 'r';
}

export function handleRefreshShortcut(
    event: KeyboardEvent,
    refresh: Refresh = () => window.triggerRefresh?.(),
): boolean {
    if (event.defaultPrevented || !isRefreshShortcut(event)) return false;

    event.preventDefault();
    if (event.repeat || hasOpenModal()) return true;

    void refresh();
    return true;
}

export function setupRefreshShortcut(): void {
    if (installed) return;
    installed = true;
    window.addEventListener('keydown', handleRefreshShortcut);
}
