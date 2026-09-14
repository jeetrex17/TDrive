import { RuntimeUnavailableError, waitForGatewayReady } from './api';
import { state } from './state';
import { configureAppActions } from './modules/app-actions';
import { mountApplication } from './modules/app-shell';
import { connectAuthEvents, initializeSession } from './modules/auth';
import { bindChannelsRenderers, refreshActiveDrive } from './modules/channels';
import { refreshFiles } from './modules/file-list';
import { runGlobalSearch } from './modules/search';
import { renderSidebar } from './modules/sidebar';
import type { AppLifecycle } from './ui/app/app-store';
import { initializeNativeTheme } from './ui/theme/native-theme';
import { initializeTheme } from './ui/theme/theme-controller';

const disposers: Array<() => void> = [initializeTheme()];
let started = false;
let stopped = false;

function registerDisposer(dispose: () => void): void {
    if (stopped) {
        dispose();
        return;
    }
    disposers.push(dispose);
}

const lifecycle: AppLifecycle = {
    async start(): Promise<void> {
        if (started) return;
        started = true;

        configureAppActions({
            refreshFiles,
            triggerRefresh: async () => {
                if (state.searchQuery.trim()) {
                    await runGlobalSearch();
                    return;
                }
                await refreshActiveDrive();
            },
            openFile: async (target) => {
                // Keep the media controller out of startup; it pulls in viewer-only dependencies.
                const viewer = await import('./modules/modals/file-viewer');
                viewer.activateFileViewerModal();
                await viewer.openFileViewer(target);
            },
            playVideo: async (target) => {
                // Video transport and player adapters stay lazy until playback is requested.
                const video = await import('./modules/modals/video');
                video.activateVideoModal();
                await video.openVideoModal(target);
            },
        });
        bindChannelsRenderers({
            onSidebarUpdate: renderSidebar,
            onActiveDriveChanged: refreshFiles,
        });

        if (!(await waitForGatewayReady())) {
            throw new RuntimeUnavailableError('App / EventsOn');
        }
        if (stopped) return;

        registerDisposer(await initializeNativeTheme());
        if (stopped) return;

        registerDisposer(connectAuthEvents());
        await initializeSession();
    },

    stop(): void {
        if (stopped) return;
        stopped = true;
        for (let index = disposers.length - 1; index >= 0; index -= 1) {
            disposers[index]();
        }
        disposers.length = 0;
    },
};

const application = mountApplication(lifecycle);

window.addEventListener('beforeunload', () => {
    lifecycle.stop();
    void application.destroy();
}, { once: true });
