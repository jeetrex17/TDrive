/**
 * An audio or subtitle track picker: a pill on the control row that steps to the
 * next track on each click, plus the full list, which the same object renders
 * into its section of the settings dock.
 *
 * The pill and the list are one object because they are one selection. The pill
 * is what a mouse reaches for mid-playback; the list is what a reader opens when
 * they want to see what the file actually contains.
 */

import { nativeTrackLabel, shortNativeTrackLabel, type NativeMediaTrack } from './media-tracks';
import type { TrackSwitching } from './player-adapters';
import type { VideoChromeController } from './video-chrome';
import type { TrackPickerDOM } from './video-dom';
import { handleMenuKeydown, menuItemMarkup, type SettingsSection } from './video-menu-dom';

export interface TrackPickerContext {
    chrome: VideoChromeController;
    /** The active player, when it is one these pills can actually drive. */
    player(): TrackSwitching | null;
    /** Whether the settings dock is currently showing this picker's section. */
    isSectionActive(section: SettingsSection): boolean;
    hideSection(): void;
    /** The control the dock was opened from, which focus returns to. */
    focusAnchor(): void;
}

export class TrackPicker {
    private tracks: NativeMediaTrack[] = [];
    private renderedSignature = '';

    constructor(
        private readonly title: string,
        private readonly offLabel: string | null,
        private readonly apply: (player: TrackSwitching, id: number | null) => void,
        private readonly els: TrackPickerDOM,
        private readonly ctx: TrackPickerContext,
    ) {
        els.button?.addEventListener('click', (event) => {
            event.stopPropagation();
            this.cycle();
            this.ctx.chrome.reveal();
        });
        els.menu?.addEventListener('click', (event) => {
            const item = (event.target as HTMLElement | null)?.closest<HTMLButtonElement>('[data-track]');
            if (!item) return;
            this.select(item.dataset.track === 'no' ? null : Number(item.dataset.track));
            this.close(true);
        });
        els.menu?.addEventListener('keydown', (event) => {
            if (this.isOpen()) handleMenuKeydown(event, this.items().filter((item) => !item.hidden), () => this.close(true));
        });
    }

    get visible(): boolean {
        return Boolean(this.els.wrap && !this.els.wrap.hidden);
    }

    get section(): SettingsSection {
        return this.offLabel === null ? 'audio' : 'subtitle';
    }

    /** Returns whether the pill appeared or disappeared, which changes the control row's height. */
    update(tracks: NativeMediaTrack[], show: boolean): boolean {
        const wasVisible = this.visible;
        this.tracks = tracks;
        if (this.els.wrap) this.els.wrap.hidden = !show;
        if (!show) {
            // Hiding the command-row pill must not take the settings panel with
            // it: the section may be on screen, and "no tracks" is a legitimate
            // thing for it to show.
            if (this.ctx.isSectionActive(this.section)) this.setMenuOpen(true);
            else this.close();
            if (tracks.length === 0) {
                this.renderedSignature = '';
                this.els.menu?.replaceChildren();
                if (this.els.menu) this.els.menu.innerHTML = `<p class="video-settings-note">No ${this.title.toLowerCase()} tracks available.</p>`;
                return wasVisible;
            }
        }
        // Re-rendering the list steals focus from whatever is inside it, so it
        // only happens when the tracks themselves changed, not on every state tick.
        const signature = JSON.stringify(this.tracks.map((track) => [track.id, track.title, track.language, track.codec]));
        if (signature !== this.renderedSignature) {
            this.renderedSignature = signature;
            this.render();
        }
        this.syncSelection();
        return !wasVisible;
    }

    isOpen(): boolean {
        return Boolean(this.els.menu?.classList.contains('is-open'));
    }

    /**
     * Shows or hides just this picker's list. It deliberately leaves the settings
     * panel alone so a section swap never closes it.
     */
    setMenuOpen(open: boolean): void {
        this.els.menu?.classList.toggle('is-open', open);
        if (!open) return;
        this.ctx.chrome.clearTimer();
        requestAnimationFrame(() => { if (this.isOpen()) this.selectedItem()?.focus({ preventScroll: true }); });
    }

    close(restoreFocus = false): void {
        if (!this.isOpen()) return;
        this.setMenuOpen(false);
        if (this.ctx.isSectionActive(this.section)) this.ctx.hideSection();
        this.ctx.chrome.settleAfterMenuClose();
        if (restoreFocus) this.ctx.focusAnchor();
    }

    contains(target: Node | null): boolean {
        return Boolean(target && (this.els.menu?.contains(target) || this.els.button?.contains(target)));
    }

    /**
     * Steps to the next track in order; an optional track (subtitles) has "off"
     * as one of the stops, a required one wraps to the first.
     */
    cycle(): void {
        if (!this.visible || this.tracks.length === 0) return;
        const current = this.currentTrack();
        const next = this.tracks[(current ? this.tracks.indexOf(current) : -1) + 1];
        const target = next?.id ?? (this.offLabel === null ? this.tracks[0].id : null);
        if (target !== (current?.id ?? null)) this.select(target);
    }

    /** Switches an optional track (subtitles) between off and its default. */
    toggle(): void {
        if (!this.visible || this.offLabel === null) return;
        const selected = this.tracks.find((track) => track.selected);
        this.select(selected ? null : this.defaultTrack()?.id ?? null);
    }

    private defaultTrack(): NativeMediaTrack | undefined {
        return this.tracks.find((track) => track.default) ?? this.tracks[0];
    }

    private currentTrack(): NativeMediaTrack | undefined {
        return this.tracks.find((track) => track.selected) ?? (this.offLabel === null ? this.defaultTrack() : undefined);
    }

    private select(id: number | null): void {
        const player = this.ctx.player();
        if (!player) return;
        if (id !== null && !this.tracks.some((track) => track.id === id)) return;
        this.apply(player, id);
        // Reflect the choice immediately; the player confirms it on the next
        // state event.
        this.tracks = this.tracks.map((track) => ({ ...track, selected: track.id === id }));
        this.syncSelection();
        this.ctx.chrome.reveal();
    }

    private items(): HTMLButtonElement[] {
        return Array.from(this.els.menu?.querySelectorAll<HTMLButtonElement>('[data-track]') || []);
    }

    private selectedItem(): HTMLButtonElement | null {
        return this.items().find((item) => item.classList.contains('is-selected')) || this.items()[0] || null;
    }

    private render(): void {
        if (!this.els.menu) return;
        const items = this.tracks.map((track, index) => menuItemMarkup(`data-track="${track.id}"`, nativeTrackLabel(track, index)));
        if (this.offLabel !== null) items.unshift(menuItemMarkup('data-track="no"', this.offLabel));
        this.els.menu.innerHTML = `<div class="video-menu-title">${this.title}</div><div class="video-track-list" role="radiogroup" aria-label="${this.title} tracks">${items.join('')}</div>`;
    }

    private syncSelection(): void {
        const current = this.currentTrack();
        const key = current ? String(current.id) : 'no';
        for (const item of this.items()) {
            const on = item.dataset.track === key;
            item.classList.toggle('is-selected', on);
            item.setAttribute('aria-checked', on ? 'true' : 'false');
            item.tabIndex = on ? 0 : -1;
        }
        const index = current ? this.tracks.indexOf(current) : -1;
        const short = current ? shortNativeTrackLabel(current, index) : this.offLabel ?? '';
        const full = current ? nativeTrackLabel(current, index) : this.offLabel ?? '';
        if (this.els.label) this.els.label.textContent = short;
        if (this.els.button) {
            const choices = this.tracks.length + (this.offLabel === null ? 0 : 1);
            this.els.button.dataset.state = current ? 'on' : 'off';
            this.els.button.title = `${this.title}: ${full}${choices > 1 ? '. Click to cycle' : ''}`;
            this.els.button.setAttribute('aria-label', this.els.button.title);
        }
    }
}
