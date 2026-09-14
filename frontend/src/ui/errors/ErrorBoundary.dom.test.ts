import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { createRawSnippet, flushSync, mount, unmount } from 'svelte';
import { get } from 'svelte/store';
import { clearAllNotifications } from '../../modules/notifications';
import { toasts } from '../notifications/toast-store';
import ErrorBoundary from './ErrorBoundary.svelte';

let host: HTMLDivElement;
let component: Record<string, unknown> | null = null;

beforeEach(() => {
    clearAllNotifications();
    host = document.createElement('div');
    document.body.appendChild(host);
});

afterEach(async () => {
    if (component) await unmount(component);
    component = null;
    host.remove();
    clearAllNotifications();
});

describe('ErrorBoundary', () => {
    it('shows redacted recovery UI, reports safely, and restores its child', async () => {
        let renderCount = 0;
        const children = createRawSnippet(() => ({
            render: () => {
                renderCount += 1;
                if (renderCount === 1) {
                    const error = new Error(
                        'token=super-secret failed at /Users/alice/TDrive/src/dashboard.ts',
                    );
                    error.stack = 'Error: token=super-secret\n    at render (/Users/alice/TDrive/src/dashboard.ts:9:2)';
                    throw error;
                }
                return '<p data-recovered="true">Files are ready</p>';
            },
        }));

        component = mount(ErrorBoundary, { target: host, props: { children } });
        flushSync();
        await Promise.resolve();
        await Promise.resolve();

        const fallback = host.querySelector<HTMLElement>('[role="alert"]');
        const heading = host.querySelector<HTMLHeadingElement>('#error-recovery-title');
        const buttons = [...host.querySelectorAll<HTMLButtonElement>('button')];

        expect(fallback).not.toBeNull();
        expect(heading?.textContent).toBe('This screen needs a reset');
        expect(document.activeElement).toBe(heading);
        expect(buttons.map((button) => button.textContent?.trim())).toEqual([
            'Restore screen',
            'Reload TDrive',
        ]);
        expect(fallback?.textContent).toContain('[redacted]');
        expect(fallback?.textContent).toContain('[local path]');
        expect(fallback?.textContent).not.toContain('super-secret');
        expect(fallback?.textContent).not.toContain('/Users/alice');
        expect(get(toasts)[0]).toMatchObject({
            id: 'render-boundary-error',
            level: 'error',
            title: 'Screen recovery needed',
            body: "TDrive couldn't finish drawing this screen. Your stored files are safe.",
        });

        buttons[0]?.click();
        await Promise.resolve();
        flushSync();

        expect(host.querySelector('[role="alert"]')).toBeNull();
        expect(host.querySelector('[data-recovered="true"]')?.textContent).toBe('Files are ready');
        expect(renderCount).toBe(2);
    });
});
