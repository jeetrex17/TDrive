import AppShell from '../ui/AppShell.svelte';
import { mountSvelte, type SvelteMountHandle } from '../ui/mount';

let shellHandle: SvelteMountHandle<Record<string, unknown>> | null = null;

export function setupAppShell(): void {
    const root = document.getElementById('app');
    if (!root || shellHandle) return;

    root.replaceChildren();
    shellHandle = mountSvelte(AppShell, { target: root, props: {} });
}
