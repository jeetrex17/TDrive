import { defineConfig, devices } from '@playwright/test';

const inCI = Boolean(process.env.CI);

export default defineConfig({
    testDir: './e2e',
    fullyParallel: true,
    forbidOnly: inCI,
    retries: 0,
    workers: inCI ? 1 : undefined,
    timeout: 15_000,
    expect: {
        timeout: 5_000,
    },
    outputDir: 'test-results',
    reporter: inCI ? 'github' : 'list',
    use: {
        baseURL: 'http://127.0.0.1:4173',
        screenshot: 'only-on-failure',
        trace: 'retain-on-failure',
        video: 'off',
    },
    projects: [
        {
            name: 'chromium',
            use: {
                ...devices['Desktop Chrome'],
                viewport: { width: 1280, height: 800 },
            },
        },
    ],
    webServer: {
        command: 'npm run build && npm run preview -- --host 127.0.0.1 --port 4173',
        url: 'http://127.0.0.1:4173',
        reuseExistingServer: !inCI,
        timeout: 120_000,
    },
});
