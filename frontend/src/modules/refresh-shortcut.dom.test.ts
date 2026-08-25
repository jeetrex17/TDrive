import { afterEach, describe, expect, it, vi } from 'vitest';
import { handleRefreshShortcut } from './refresh-shortcut';

function shortcut(overrides: KeyboardEventInit = {}): KeyboardEvent {
    return new KeyboardEvent('keydown', {
        key: 'r',
        ctrlKey: true,
        cancelable: true,
        ...overrides,
    });
}

afterEach(() => {
    document.body.replaceChildren();
});

describe('handleRefreshShortcut', () => {
    it.each([
        ['macOS', shortcut({ metaKey: true, ctrlKey: false })],
        ['Windows and Linux', shortcut()],
    ])('refreshes with the %s shortcut and prevents WebView reload', (_platform, event) => {
        const refresh = vi.fn();

        const handled = handleRefreshShortcut(event, refresh);

        expect(handled).toBe(true);
        expect(event.defaultPrevented).toBe(true);
        expect(refresh).toHaveBeenCalledTimes(1);
    });

    it('ignores unrelated, prevented, and alternate-modifier events', () => {
        const refresh = vi.fn();
        const prevented = shortcut();
        prevented.preventDefault();

        for (const event of [
            shortcut({ key: 'f' }),
            shortcut({ ctrlKey: false, metaKey: false }),
            shortcut({ altKey: true }),
            prevented,
        ]) {
            expect(handleRefreshShortcut(event, refresh)).toBe(false);
        }
        expect(refresh).not.toHaveBeenCalled();
    });

    it('consumes repeated shortcuts and shortcuts behind an open modal without refreshing', () => {
        const refresh = vi.fn();
        const repeated = shortcut({ repeat: true });

        expect(handleRefreshShortcut(repeated, refresh)).toBe(true);
        expect(repeated.defaultPrevented).toBe(true);

        const dialog = document.createElement('div');
        dialog.setAttribute('role', 'dialog');
        dialog.setAttribute('aria-modal', 'true');
        document.body.append(dialog);
        const modalEvent = shortcut();

        const handled = handleRefreshShortcut(modalEvent, refresh);

        expect(handled).toBe(true);
        expect(modalEvent.defaultPrevented).toBe(true);
        expect(refresh).not.toHaveBeenCalled();
    });
});
