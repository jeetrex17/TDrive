// The shell publishes the window insets Android's WebView does not report, and
// takes them back down when it goes.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const isAndroidPlatform = vi.hoisted(() => vi.fn(() => true));
const getSafeAreaInsets = vi.hoisted(() => vi.fn());
vi.mock('../../api', () => ({ isAndroidPlatform, getSafeAreaInsets }));

import { activateSafeArea } from './safe-area';

const EDGES = ['top', 'bottom', 'left', 'right'] as const;

function published(edge: (typeof EDGES)[number]): string {
    return document.documentElement.style.getPropertyValue(`--mobile-inset-${edge}`);
}

/** Answers the next read, and hands back the resolve so it can answer late. */
function defer(insets: Record<string, number>): () => void {
    let release = (): void => {};
    getSafeAreaInsets.mockImplementationOnce(() => new Promise<unknown>((resolve) => {
        release = () => resolve(insets);
    }));
    return () => release();
}

beforeEach(() => {
    isAndroidPlatform.mockReturnValue(true);
    getSafeAreaInsets.mockReset();
    window.devicePixelRatio = 2;
    for (const edge of EDGES) document.documentElement.style.removeProperty(`--mobile-inset-${edge}`);
});

afterEach(() => {
    for (const edge of EDGES) document.documentElement.style.removeProperty(`--mobile-inset-${edge}`);
});

describe('activateSafeArea', () => {
    it('publishes all four edges in CSS pixels', async () => {
        // Turned, the notch takes one side and the navigation bar the other,
        // and env() answers zero for both on this WebView.
        getSafeAreaInsets.mockResolvedValue({ top: 96, bottom: 48, left: 88, right: 24 });
        const dispose = activateSafeArea();
        await vi.waitFor(() => expect(published('left')).toBe('44px'));

        expect(published('top')).toBe('48px');
        expect(published('bottom')).toBe('24px');
        expect(published('right')).toBe('12px');

        dispose();
    });

    it('drops the reservation when the shell goes', async () => {
        getSafeAreaInsets.mockResolvedValue({ top: 96, bottom: 48, left: 0, right: 0 });
        const dispose = activateSafeArea();
        await vi.waitFor(() => expect(published('top')).toBe('48px'));

        dispose();

        for (const edge of EDGES) expect(published(edge)).toBe('');
    });

    it('ignores a reading that lands after the shell has gone', async () => {
        // The read is asynchronous, so one already in flight resolves after the
        // teardown has cleared the variables and would reserve the space forever.
        const answer = defer({ top: 96, bottom: 48, left: 0, right: 0 });
        const dispose = activateSafeArea();
        dispose();

        answer();
        await Promise.resolve();
        await Promise.resolve();

        expect(published('top')).toBe('');
    });

    it('ignores a reading overtaken by a newer one', async () => {
        // A rotation fires a second read; whichever answers last must not be
        // the one that wins, or the layout keeps the old window's edges.
        const answerFirst = defer({ top: 200, bottom: 0, left: 0, right: 0 });
        const dispose = activateSafeArea();
        getSafeAreaInsets.mockResolvedValue({ top: 96, bottom: 0, left: 0, right: 0 });
        window.dispatchEvent(new Event('orientationchange'));
        await vi.waitFor(() => expect(published('top')).toBe('48px'));

        answerFirst();
        await Promise.resolve();
        await Promise.resolve();

        expect(published('top')).toBe('48px');
        dispose();
    });

    it('leaves iOS to its own env()', () => {
        isAndroidPlatform.mockReturnValue(false);
        document.documentElement.style.setProperty('--mobile-inset-top', '48px');

        const dispose = activateSafeArea();

        expect(published('top')).toBe('');
        expect(getSafeAreaInsets).not.toHaveBeenCalled();
        dispose();
    });
});
