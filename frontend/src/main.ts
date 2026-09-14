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
import { deriveVideoPlaylist } from './modules/video/video-playlist';
import { getInteractiveFileListRows } from './ui/file-list/file-list-store';
import type { FileListFileRow } from './ui/file-list/types';

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
                let playlistContext:
                    | {
                        items: Array<{ id: number; key: string; name: string; size?: number; encrypted?: boolean }>;
                        currentIndex: number;
                        title: string;
                    }
                    | undefined;
                if (target && !state.searchQuery.trim() && state.virtualView === null) {
                    const fileRows = getInteractiveFileListRows()
                        .filter((row): row is FileListFileRow => row.kind === 'file');
                    const playlist = deriveVideoPlaylist(fileRows, target.id);
                    if (playlist.currentIndex >= 0) {
                        const folder = state.folderPath[state.folderPath.length - 1]?.name
                            || state.activeChannel?.title
                            || 'Folder';
                        playlistContext = {
                            items: playlist.items.map((item) => ({
                                id: Number(item.id),
                                key: item.key,
                                name: item.name,
                                size: item.size,
                                encrypted: item.encrypted,
                            })),
                            currentIndex: playlist.currentIndex,
                            title: `Videos in ${folder}`,
                        };
                    }
                }
                const video = await import('./modules/modals/video');
                video.activateVideoModal();
                if (playlistContext) {
                    await video.openVideoModal(target, playlistContext);
                    return;
                }
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
