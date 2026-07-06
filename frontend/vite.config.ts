/// <reference types="vitest/config" />
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vite';

export default defineConfig({
    plugins: [svelte()],
    test: {
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
