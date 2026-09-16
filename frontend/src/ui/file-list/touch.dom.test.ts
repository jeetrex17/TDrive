import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { bindLongPress, bindPullToRefresh } from './touch';

// Haptics now go through one binding rather than a per-platform runtime call,
// so the mock only has to silence that.
const playHaptic = vi.hoisted(() => vi.fn());
vi.mock('../../api', () => ({
    playHaptic,
    // The row swipe shares its verdict with the pull, and only Android guards
    // its trailing edge.
    isAndroidPlatform: () => false,
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

// clientX matters as much as clientY now: the pull and the row swipe settle
// which of them owns a gesture from the same two numbers.
function touch(type: string, clientY: number, clientX = 200, cancelable = true): Event {
    const event = new Event(type, { bubbles: true, cancelable });
    const touches = type === 'touchend' ? [] : [{ clientX, clientY }];
    Object.assign(event, { touches, changedTouches: [{ clientX, clientY }] });
    host.dispatchEvent(event);
    return event;
}

beforeEach(() => {
    playHaptic.mockClear();
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

    it('leaves the press alone away from the rows', () => {
        // Swallowed across the whole host, this also took selection and lookup
        // off the group headings and the empty space around the list, where the
        // long press opens nothing of ours to replace them.
        const heading = document.createElement('h2');
        host.append(heading);
        bindLongPress(host, '.drive-row', vi.fn());

        const native = new MouseEvent('contextmenu', { bubbles: true, cancelable: true });
        Object.defineProperty(native, 'isTrusted', { value: true });
        heading.dispatchEvent(native);

        expect(native.defaultPrevented).toBe(false);
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

    it('leaves a sideways drag to the row that owns it', () => {
        // Down and to the left used to run both recognisers: the row opened
        // while the refresh ring slid in behind it.
        const refresh = vi.fn();
        bindPullToRefresh(host, refresh);
        const indicator = host.querySelector<HTMLElement>('.pull-refresh')!;

        touch('touchstart', 100, 200);
        touch('touchmove', 130, 140);
        touch('touchmove', 180, 60);

        expect(indicator.classList.contains('is-pulling')).toBe(false);
        expect(indicator.style.getPropertyValue('--pull')).not.toBe('40px');

        touch('touchend', 180, 60);
        expect(refresh).not.toHaveBeenCalled();
    });

    it('keeps the pull once the gesture has been read as vertical', () => {
        // The verdict is taken once: a pull that drifts sideways later stays a
        // pull rather than being handed over halfway down.
        bindPullToRefresh(host, vi.fn());
        const indicator = host.querySelector<HTMLElement>('.pull-refresh')!;

        touch('touchstart', 100, 200);
        touch('touchmove', 140, 198);
        touch('touchmove', 180, 120);

        expect(indicator.classList.contains('is-pulling')).toBe(true);
        expect(indicator.style.getPropertyValue('--pull')).toBe('40px');
    });

    it('ticks once per pull however often the threshold is crossed', () => {
        // A thumb resting on the line crosses it over and over; a buzz for
        // every wobble is what teaches people to turn haptics off.
        bindPullToRefresh(host, vi.fn());

        touch('touchstart', 100);
        touch('touchmove', 240);
        expect(playHaptic).toHaveBeenCalledTimes(1);

        touch('touchmove', 226);
        touch('touchmove', 240);
        touch('touchmove', 224);
        touch('touchmove', 250);
        expect(playHaptic).toHaveBeenCalledTimes(1);

        // A new gesture gets its own tick.
        touch('touchmove', 90);
        touch('touchend', 90);
        touch('touchstart', 100);
        touch('touchmove', 240);
        expect(playHaptic).toHaveBeenCalledTimes(2);
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
