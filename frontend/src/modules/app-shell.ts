import AppRoot from '../ui/app/AppRoot.svelte';
import type { AppLifecycle } from '../ui/app/app-store';
import { mountSvelte } from '../ui/mount';

export function mountApplication(lifecycle: AppLifecycle) {
    const target = document.getElementById('app');
    if (!target) throw new Error('Application root #app is missing.');

    target.replaceChildren();
    return mountSvelte(AppRoot, {
        target,
        props: { lifecycle },
    });
}
