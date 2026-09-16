import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { bindLongPress, bindPullToRefresh } from './touch';

// Haptics now go through one binding rather than a per-platform runtime call,
// so the mock only has to silence that.
vi.mock('../../api', () => ({
    playHaptic: vi.fn(),
}));

let host: HTMLElement;
let row: HTMLElement;

function pointer(type: string, target: EventTarget, init: PointerEventInit = {}): void {
    target.dispatchEvent(new PointerEvent(type, {
        pointerId: 1,
        pointerType: 'touch',
        isPrimary: true,
        clientX: 100,
        clientY: 100,
        bubbles: true,
        cancelable: true,
        ...init,
    }));
}

function touch(type: string, clientY: number, cancelable = true): Event {
    const event = new Event(type, { bubbles: true, cancelable });
    const touches = type === 'touchend' ? [] : [{ clientY }];
    Object.assign(event, { touches, changedTouches: [{ clientY }] });
    host.dispatchEvent(event);
    return event;
}

beforeEach(() => {
    vi.useFakeTimers();
    host = document.createElement('div');
    host.id = 'file-list';
    row = document.createElement('div');
    row.className = 'drive-row';
    row.dataset.type = 'file';
    const icon = document.createElement('span');
    icon.className = 'file-type-icon';
    row.append(icon);
    host.append(row);
    document.body.append(host);
});

afterEach(() => {
    vi.useRealTimers();
    host.remove();
});

describe('bindLongPress', () => {
    it('fires after 350ms with the row, the press point and the pressed element', () => {
        const onLongPress = vi.fn();
        const unbind = bindLongPress(host, '.drive-row', onLongPress);
        const icon = row.querySelector('.file-type-icon')!;

        pointer('pointerdown', icon, { clientX: 40, clientY: 60 });
        vi.advanceTimersByTime(340);
        expect(onLongPress).not.toHaveBeenCalled();
        vi.advanceTimersByTime(20);
        expect(onLongPress).toHaveBeenCalledWith(row, 40, 60, icon);
        unbind();
    });

    it('swallows the click the browser synthesises after a press', () => {
        const onLongPress = vi.fn();
        const onClick = vi.fn();
        host.addEventListener('click', onClick);
        bindLongPress(host, '.drive-row', onLongPress);

        pointer('pointerdown', row);
        vi.advanceTimersByTime(350);
        pointer('pointerup', row);
        row.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
        expect(onClick).not.toHaveBeenCalled();

        // The next tap is an ordinary click again.
        row.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
        expect(onClick).toHaveBeenCalledTimes(1);
    });

    it('is cancelled by lifting, moving past the slop, or a mouse', () => {
        const onLongPress = vi.fn();
        bindLongPress(host, '.drive-row', onLongPress);

        pointer('pointerdown', row);
        vi.advanceTimersByTime(200);
        pointer('pointerup', row);
        vi.advanceTimersByTime(300);

        pointer('pointerdown', row);
        pointer('pointermove', row, { clientX: 100, clientY: 130 });
        vi.advanceTimersByTime(400);

        pointer('pointerdown', row, { pointerType: 'mouse' });
        vi.advanceTimersByTime(400);
        expect(onLongPress).not.toHaveBeenCalled();
    });

    it('suppresses the native context menu but lets a synthetic one through', () => {
        const seen = vi.fn();
        host.addEventListener('contextmenu', seen);
        bindLongPress(host, '.drive-row', vi.fn());

        // Untrusted events are all a test can dispatch, so the trusted branch
        // is exercised through the same listener by faking the flag.
        const synthetic = new MouseEvent('contextmenu', { bubbles: true, cancelable: true });
        row.dispatchEvent(synthetic);
        expect(seen).toHaveBeenCalledTimes(1);

        const native = new MouseEvent('contextmenu', { bubbles: true, cancelable: true });
        Object.defineProperty(native, 'isTrusted', { value: true });
        row.dispatchEvent(native);
        expect(seen).toHaveBeenCalledTimes(1);
        expect(native.defaultPrevented).toBe(true);
    });
});

describe('bindPullToRefresh', () => {
    it('adds the ring, arms past the threshold and refreshes on release', async () => {
        const refresh = vi.fn(() => Promise.resolve());
        bindPullToRefresh(host, refresh);
        const indicator = host.querySelector<HTMLElement>('.pull-refresh')!;
        expect(indicator).not.toBeNull();
        expect(host.firstElementChild).toBe(indicator);

        touch('touchstart', 100);
        const move = touch('touchmove', 160);
        expect(move.defaultPrevented).toBe(true);
        expect(indicator.classList.contains('is-pulling')).toBe(true);
        expect(indicator.classList.contains('is-armed')).toBe(false);
        expect(indicator.style.getPropertyValue('--pull')).toBe('30px');

        touch('touchmove', 260);
        expect(indicator.classList.contains('is-armed')).toBe(true);

        touch('touchend', 260);
        expect(indicator.classList.contains('is-refreshing')).toBe(true);
        await Promise.resolve();
        expect(refresh).toHaveBeenCalledTimes(1);

        await vi.advanceTimersByTimeAsync(600);
        expect(indicator.classList.contains('is-refreshing')).toBe(false);
        expect(indicator.classList.contains('is-pulling')).toBe(false);
    });

    it('settles without refreshing when released early or scrolled', () => {
        const refresh = vi.fn();
        bindPullToRefresh(host, refresh);
        const indicator = host.querySelector<HTMLElement>('.pull-refresh')!;

        touch('touchstart', 100);
        touch('touchmove', 150);
        touch('touchend', 150);
        expect(refresh).not.toHaveBeenCalled();
        expect(indicator.classList.contains('is-pulling')).toBe(false);

        Object.defineProperty(host, 'scrollTop', { value: 40, configurable: true });
        touch('touchstart', 100);
        touch('touchmove', 260);
        touch('touchend', 260);
        expect(refresh).not.toHaveBeenCalled();
    });
});
