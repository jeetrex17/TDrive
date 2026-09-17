import { defineConfig } from '@playwright/test';
import base from './playwright.config';

// WKWebView and WebKitGTK share this engine. Native-host lifecycle and memory
// measurements still need device tests; these protect the shared gallery UI.
export default defineConfig({
    ...base,
    testMatch: 'app.spec.ts',
    grep: /gallery|photo gallery/,
    projects: [{
        name: 'webkit',
        use: { browserName: 'webkit', viewport: { width: 1280, height: 800 } },
    }],
});
