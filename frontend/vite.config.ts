/// <reference types="vitest/config" />
import { resolve } from 'node:path';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vite';

export default defineConfig({
    plugins: [svelte()],
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
            include: ['src/ui/theme/**'],
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
