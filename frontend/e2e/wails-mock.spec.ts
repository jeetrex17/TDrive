import { bootTDrive, expect, test } from './wails-mock';

// These requests exercise the mock itself, so use the base Playwright fixture:
// the strict fixture intentionally fails a journey containing unexpected calls.
import { test as harnessTest } from '@playwright/test';

harnessTest('rejects unknown and unconfigured application and runtime calls', async ({ page }) => {
    await bootTDrive(page);
    const responses = await page.evaluate(async () => {
        const request = async (args: Record<string, unknown>) => {
            const response = await fetch('/wails/runtime', {
                method: 'POST',
                body: JSON.stringify({ object: 0, method: 0, args: { 'call-id': 'probe', ...args } }),
            });
            return { status: response.status, body: await response.json() };
        };
        return Promise.all([
            request({ methodID: -1 }),
            request({ methodName: 'DeleteFile', args: [42] }),
            fetch('/wails/runtime', {
                method: 'POST', body: JSON.stringify({ object: 999, method: 0 }),
            }).then(async (response) => ({ status: response.status, body: await response.json() })),
        ]);
    });
    for (const response of responses) {
        expect(response.status).toBe(500);
        expect(response.body.message).toContain('wails-mock:');
    }
});

test('rejects plans for methods missing from generated bindings', async ({ page }) => {
    const mock = await bootTDrive(page);
    await expect(mock.setPlan('MissingMethod', { kind: 'resolve', value: null, delayMs: 0 }))
        .rejects.toThrow('no generated binding');
});
