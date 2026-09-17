/// <reference types="vitest/config" />
import { resolve } from 'node:path';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vite';
import wails from '@wailsio/runtime/plugins/vite';

export default defineConfig({
    plugins: [svelte(), wails('./bindings')],
    // wails3 dev proxies to 127.0.0.1; without this Vite 8 binds only ::1.
    server: {
        host: '127.0.0.1',
        port: Number(process.env.WAILS_VITE_PORT) || 9245,
        strictPort: true,
    },
    build: {
        rollupOptions: {
            input: {
                main: resolve(__dirname, 'index.html'),
                pdfViewer: resolve(__dirname, 'pdf-viewer.html'),
            },
        },
    },
    test: {
        coverage: {
            provider: 'v8',
            include: [
                // Gate behavior-heavy modules where missed branches are user-visible.
                // Thin Wails forwarding stays outside the percentage gate: testing
                // mock echoes would inflate coverage without defending a contract.
                'src/modules/errors.ts',
                'src/modules/media-types.ts',
                'src/modules/gallery.ts',
                'src/modules/gallery-policy.ts',
                'src/modules/renditions/**',
                'src/api/gallery.ts',
                'src/api/renditions.ts',
                'src/ui/gallery/gallery-controller.ts',
                'src/ui/gallery/gallery-layout.ts',
                'src/ui/gallery/gallery-source.ts',
                'src/modules/modals/preview-info.ts',
                'src/modules/video/media-tracks.ts',
                'src/ui/auth/auth-store.ts',
                'src/ui/auth/personal-drive-store.ts',
                'src/ui/sidebar/sidebar-store.ts',
                'src/ui/theme/**',
                'src/ui/updates/update-model.ts',
                'src/ui/updates/update-store.ts',
                'src/ui/video/**',
            ],
            thresholds: {
                branches: 80,
                functions: 80,
                lines: 80,
                statements: 80,
            },
        },
        projects: [
            // Fast server-render smoke tests (the default): assert markup from
            // svelte/server without a DOM.
            {
                extends: true,
                test: {
                    name: 'ssr',
                    include: ['src/**/*.test.{js,ts}'],
                    exclude: ['src/**/*.dom.test.ts'],
                },
            },
            // Browser-behavior tests (*.dom.test.ts): mount real components in
            // happy-dom. The browser condition picks Svelte's client build, so
            // mount/flushSync and effects work.
            {
                extends: true,
                resolve: { conditions: ['browser'] },
                test: {
                    name: 'dom',
                    environment: 'happy-dom',
                    include: ['src/**/*.dom.test.ts'],
                },
            },
        ],
    },
});
