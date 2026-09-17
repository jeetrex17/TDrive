import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { NativeMediaTrack } from './media-tracks';
import type { TrackSwitching } from './player-adapters';
import { TrackPicker, type TrackPickerHost } from './track-picker';
import type { TrackPickerDOM } from './video-dom';

function track(overrides: Partial<NativeMediaTrack> & { id: number }): NativeMediaTrack {
    return {
        type: 'audio',
        selected: false,
        default: false,
        forced: false,
        ...overrides,
    };
}

function mountPicker(): TrackPickerDOM {
    const wrap = document.createElement('div');
    const button = document.createElement('button');
    const label = document.createElement('span');
    const menu = document.createElement('div');
    // The markup ships the pill hidden: it only appears once a file turns out
    // to have tracks of this kind, which is what `update` reports on.
    wrap.hidden = true;
    wrap.append(button, menu);
    button.append(label);
    document.body.append(wrap);
    return { wrap, button, label, menu };
}

function makePlayer() {
    return {
        setAudioTrack: vi.fn((_id: number): void => {}),
        setSubtitleTrack: vi.fn((_id: number | null): void => {}),
    };
}

function makeHost() {
    return {
        openSettingsSection: vi.fn((): string | null => section),
        hideSettingsPanel: vi.fn(),
        holdChrome: vi.fn(),
        releaseChrome: vi.fn(),
        revealChrome: vi.fn(),
        trackSwitchingPlayer: vi.fn((): TrackSwitching | null => player),
        returnFocus: vi.fn(),
    };
}

let dom: TrackPickerDOM;
let player: ReturnType<typeof makePlayer>;
let host: ReturnType<typeof makeHost>;
let section: string | null;

beforeEach(() => {
    dom = mountPicker();
    section = null;
    player = makePlayer();
    host = makeHost();
});

afterEach(() => {
    document.body.replaceChildren();
});

function audioPicker() {
    return new TrackPicker('Audio', null, (target, id) => {
        if (id !== null) target.setAudioTrack(id);
    }, dom, host);
}

function subtitlePicker() {
    return new TrackPicker('Subtitles', 'Off', (target, id) => target.setSubtitleTrack(id), dom, host);
}

const englishAndJapanese = [
    track({ id: 1, language: 'eng', selected: true, default: true }),
    track({ id: 2, language: 'jpn' }),
];

describe('showing and hiding the track pill', () => {
    it('reports the pill appearing, so the caller can re-measure the control row', () => {
        const picker = audioPicker();
        expect(picker.update(englishAndJapanese, true)).toBe(true);
        expect(picker.visible).toBe(true);
    });

    it('reports the pill disappearing for the same reason', () => {
        const picker = audioPicker();
        picker.update(englishAndJapanese, true);
        expect(picker.update([], false)).toBe(true);
        expect(picker.visible).toBe(false);
    });

    it('stays quiet when the same tracks arrive again, which is every state event during playback', () => {
        const picker = audioPicker();
        picker.update(englishAndJapanese, true);
        expect(picker.update(englishAndJapanese, true)).toBe(false);
    });

    it('does not rebuild the menu when nothing about the tracks changed', () => {
        const picker = audioPicker();
        picker.update(englishAndJapanese, true);
        const first = dom.menu!.querySelector('[data-track="1"]');
        picker.update([...englishAndJapanese], true);
        expect(dom.menu!.querySelector('[data-track="1"]')).toBe(first);
    });

    it('rebuilds the menu once a track is actually added', () => {
        const picker = audioPicker();
        picker.update(englishAndJapanese, true);
        picker.update([...englishAndJapanese, track({ id: 3, language: 'ger' })], true);
        expect(dom.menu!.querySelectorAll('[data-track]')).toHaveLength(3);
    });

    it('says so plainly when the file carries no tracks of this kind', () => {
        const picker = audioPicker();
        picker.update([], false);
        expect(dom.menu!.textContent).toBe('No audio tracks available.');
    });

    it('keeps its section of the settings panel open when the pill goes away, because "none" is worth showing', () => {
        section = 'audio';
        const picker = audioPicker();
        picker.update([], false);
        expect(picker.isOpen()).toBe(true);
    });
});

describe('choosing a track', () => {
    it('applies the pick to the player and shows it at once, before the player confirms', () => {
        const picker = audioPicker();
        picker.update(englishAndJapanese, true);
        dom.menu!.querySelector<HTMLButtonElement>('[data-track="2"]')!.click();
        expect(player.setAudioTrack).toHaveBeenCalledWith(2);
        expect(dom.menu!.querySelector('[data-track="2"]')?.classList.contains('is-selected')).toBe(true);
    });

    it('ignores a pick when no player can switch tracks right now', () => {
        host.trackSwitchingPlayer = vi.fn((): TrackSwitching | null => null);
        const picker = new TrackPicker('Audio', null, (target, id) => {
            if (id !== null) target.setAudioTrack(id);
        }, dom, host);
        picker.update(englishAndJapanese, true);
        dom.menu!.querySelector<HTMLButtonElement>('[data-track="2"]')!.click();
        expect(player.setAudioTrack).not.toHaveBeenCalled();
    });

    it('cycles to the next track when the pill is clicked', () => {
        const picker = audioPicker();
        picker.update(englishAndJapanese, true);
        picker.cycle();
        expect(player.setAudioTrack).toHaveBeenCalledWith(2);
    });

    it('wraps a required track back to the first, since audio can never be off', () => {
        const picker = audioPicker();
        picker.update([
            track({ id: 1, language: 'eng' }),
            track({ id: 2, language: 'jpn', selected: true }),
        ], true);
        picker.cycle();
        expect(player.setAudioTrack).toHaveBeenCalledWith(1);
    });

    it('cycles an optional track through off before coming back round', () => {
        const picker = subtitlePicker();
        picker.update([track({ id: 2, type: 'subtitle', selected: true })], true);
        picker.cycle();
        expect(player.setSubtitleTrack).toHaveBeenCalledWith(null);
    });

    it('does nothing at all when the pill is not on screen', () => {
        const picker = audioPicker();
        picker.update(englishAndJapanese, false);
        picker.cycle();
        expect(player.setAudioTrack).not.toHaveBeenCalled();
    });

    it('turns subtitles on to their default track and back off again with the C shortcut', () => {
        const picker = subtitlePicker();
        picker.update([
            track({ id: 3, type: 'subtitle' }),
            track({ id: 4, type: 'subtitle', default: true }),
        ], true);

        picker.toggle();
        expect(player.setSubtitleTrack).toHaveBeenLastCalledWith(4);

        picker.toggle();
        expect(player.setSubtitleTrack).toHaveBeenLastCalledWith(null);
    });

    it('leaves a required track alone when asked to toggle, since there is no off to go to', () => {
        const picker = audioPicker();
        picker.update(englishAndJapanese, true);
        picker.toggle();
        expect(player.setAudioTrack).not.toHaveBeenCalled();
    });
});

describe('what the pill says', () => {
    it('shows the short label on the pill and the full one in its description', () => {
        const picker = audioPicker();
        picker.update(englishAndJapanese, true);
        expect(dom.label!.textContent).toBeTruthy();
        expect(dom.button!.title).toContain('Audio:');
        expect(dom.button!.getAttribute('aria-label')).toBe(dom.button!.title);
    });

    it('offers to cycle only when there is more than one choice', () => {
        const picker = audioPicker();
        picker.update([track({ id: 1, language: 'eng', selected: true })], true);
        expect(dom.button!.title).not.toContain('Click to cycle');
        picker.update(englishAndJapanese, true);
        expect(dom.button!.title).toContain('Click to cycle');
    });

    it('reads as off when an optional track has no selection', () => {
        const picker = subtitlePicker();
        picker.update([track({ id: 3, type: 'subtitle' })], true);
        expect(dom.label!.textContent).toBe('Off');
        expect(dom.button!.dataset.state).toBe('off');
    });

    it('keeps only the selected item in the tab order, so one Tab leaves the list', () => {
        const picker = audioPicker();
        picker.update(englishAndJapanese, true);
        const items = Array.from(dom.menu!.querySelectorAll<HTMLButtonElement>('[data-track]'));
        expect(items.filter((item) => item.tabIndex === 0)).toHaveLength(1);
    });
});

describe('the menu and the player chrome', () => {
    it('holds the chrome open while the list is up, so it cannot fade out from under the reader', () => {
        const picker = audioPicker();
        picker.update(englishAndJapanese, true);
        picker.setMenuOpen(true);
        expect(host.holdChrome).toHaveBeenCalled();
    });

    it('lets the chrome resume fading once the list closes', () => {
        const picker = audioPicker();
        picker.update(englishAndJapanese, true);
        picker.setMenuOpen(true);
        picker.close();
        expect(host.releaseChrome).toHaveBeenCalled();
    });

    it('closes the settings panel with the list only when the panel is showing this picker', () => {
        const picker = audioPicker();
        picker.update(englishAndJapanese, true);

        section = 'picture';
        picker.setMenuOpen(true);
        picker.close();
        expect(host.hideSettingsPanel).not.toHaveBeenCalled();

        section = 'audio';
        picker.setMenuOpen(true);
        picker.close();
        expect(host.hideSettingsPanel).toHaveBeenCalledTimes(1);
    });

    it('returns focus to the settings control when it closes itself after a pick', () => {
        const picker = audioPicker();
        picker.update(englishAndJapanese, true);
        picker.setMenuOpen(true);
        picker.close(true);
        expect(host.returnFocus).toHaveBeenCalledTimes(1);
    });

    it('does nothing when closed while already closed', () => {
        const picker = audioPicker();
        picker.update(englishAndJapanese, true);
        picker.close(true);
        expect(host.returnFocus).not.toHaveBeenCalled();
    });

    it('knows whether a click landed on its own pill or list, which is how outside clicks dismiss it', () => {
        const picker = audioPicker();
        picker.update(englishAndJapanese, true);
        expect(picker.contains(dom.button)).toBe(true);
        expect(picker.contains(dom.menu!.querySelector('[data-track="1"]'))).toBe(true);
        expect(picker.contains(document.body)).toBe(false);
        expect(picker.contains(null)).toBe(false);
    });
});
