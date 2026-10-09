/** App controls for protected streams, shared by viewers and the PDF frame. */
export function bindViewOnlyGuards(host: HTMLElement, protectedContent: () => boolean, allowContextActions = false): () => void {
    const block = (event: Event): void => {
        if (!protectedContent()) return;
        event.preventDefault();
        if (!(allowContextActions && event.type === 'contextmenu')) event.stopPropagation();
    };
    const keydown = (event: KeyboardEvent): void => {
        if ((event.ctrlKey || event.metaKey) && ['a', 'c', 'x', 's', 'p'].includes(event.key.toLowerCase())) block(event);
    };
    const events = ['contextmenu', 'copy', 'cut', 'dragstart'];
    for (const name of events) host.addEventListener(name, block, true);
    host.addEventListener('keydown', keydown, true);
    return () => {
        for (const name of events) host.removeEventListener(name, block, true);
        host.removeEventListener('keydown', keydown, true);
    };
}


let screenProtectionOwners: readonly symbol[] = [];

/** Keep native protection enabled until the last protected viewer releases it. */
export function acquireScreenProtection(setProtection: (enabled: boolean) => void): () => void {
    const owner = Symbol('protected-viewer');
    const first = screenProtectionOwners.length === 0;
    screenProtectionOwners = [...screenProtectionOwners, owner];
    if (first) setProtection(true);
    return () => {
        if (!screenProtectionOwners.includes(owner)) return;
        screenProtectionOwners = screenProtectionOwners.filter((current) => current !== owner);
        if (screenProtectionOwners.length === 0) setProtection(false);
    };
}
