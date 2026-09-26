/**
 * One audio or subtitle track chooser: the pill on the control row and the list
 * it renders into the settings dock.
 *
 * The two pills are the same widget twice over, differing only in what they are
 * called, whether "off" is one of the choices, and which player call applies a
 * pick -- so they are one class, constructed twice. Keeping it out of the video
 * controller matters because this is the only part of the player with a
 * genuinely independent piece of state: which track the reader last chose,
 * shown optimistically before the player has confirmed it. Everything the
 * picker needs from the controller (what the chrome is doing, which section of
 * the settings panel is up, which player can switch tracks right now) arrives
 * through `TrackPickerHost`, so the picker never reads the controller's
 * variables and the controller never reaches into the picker's.
 */

import { nativeTrackLabel, shortNativeTrackLabel, type NativeMediaTrack } from "./media-tracks";
import { handleMenuKeydown, menuItemMarkup } from "./menu-keyboard";
import type { TrackSwitching } from "./player-adapters";
import type { TrackPickerDOM } from "./video-dom";

/** The settings-panel sections a picker can be the content of. */
export type TrackPickerSection = "audio" | "subtitle";

export interface TrackPickerHost {
    /** Which settings section is on screen, or null when the panel is closed. */
    openSettingsSection(): string | null;
    /** Close the settings panel, when this picker's own section is what it is showing. */
    hideSettingsPanel(): void;
    /** Keep the chrome on screen for as long as a menu is open. */
    holdChrome(): void;
    /** Let the chrome resume fading, if the player is in a state where it should. */
    releaseChrome(): void;
    /** Bring the chrome back and restart its fade timer after an interaction. */
    revealChrome(): void;
    /** The player these pills can drive, or null when nothing can switch tracks. */
    trackSwitchingPlayer(): TrackSwitching | null;
    /** Send focus back to the control that opens the settings dock. */
    returnFocus(): void;
}

export class TrackPicker {
    private tracks: NativeMediaTrack[] = [];
    private renderedSignature = "";

    constructor(
        private readonly title: string,
        private readonly offLabel: string | null,
        private readonly apply: (player: TrackSwitching, id: number | null) => void,
        private readonly els: TrackPickerDOM,
        private readonly host: TrackPickerHost,
    ) {
        els.button?.addEventListener("click", (event) => {
            event.stopPropagation();
            this.cycle();
            this.host.revealChrome();
        });
        els.menu?.addEventListener("click", (event) => {
            const item = (event.target as HTMLElement | null)?.closest<HTMLButtonElement>("[data-track]");
            if (!item) return;
            this.select(item.dataset.track === "no" ? null : Number(item.dataset.track));
            this.close(true);
        });
        els.menu?.addEventListener("keydown", (event) => {
            if (this.isOpen()) handleMenuKeydown(event, this.items().filter((item) => !item.hidden), () => this.close(true));
        });
    }

    get visible() {
        return Boolean(this.els.wrap && !this.els.wrap.hidden);
    }

    /**
     * Take a new track list and say whether the pill appeared or disappeared,
     * which the caller uses to re-measure the control row.
     *
     * This runs on every player state event. The signature check is what keeps
     * that cheap: re-rendering the menu on each event would throw away the
     * focused element several times a second, so the list is rebuilt only when
     * the tracks themselves changed and the common case is a string compare.
     */
    update(tracks: NativeMediaTrack[], show: boolean): boolean {
        const wasVisible = this.visible;
        this.tracks = tracks;
        if (this.els.wrap) this.els.wrap.hidden = !show;
        if (!show) {
            // Hiding the command-row pill must not take the settings panel with
            // it: the section may be on screen, and "no tracks" is a legitimate
            // thing for it to show.
            if (this.host.openSettingsSection() === this.section) this.setMenuOpen(true);
            else this.close();
            if (tracks.length === 0) {
                this.renderedSignature = "";
                this.els.menu?.replaceChildren();
                if (this.els.menu) this.els.menu.innerHTML = `<p class="video-settings-note">No ${this.title.toLowerCase()} tracks available.</p>`;
                return wasVisible;
            }
        }
        const signature = JSON.stringify(this.tracks.map((track) => [track.id, track.title, track.language, track.codec]));
        if (signature !== this.renderedSignature) {
            this.renderedSignature = signature;
            this.render();
        }
        this.syncSelection();
        return !wasVisible;
    }

    isOpen() {
        return Boolean(this.els.menu?.classList.contains("is-open"));
    }

    get section(): TrackPickerSection {
        return this.offLabel === null ? "audio" : "subtitle";
    }

    // setMenuOpen shows or hides just this picker's list. It deliberately
    // leaves the settings panel alone so a section swap never closes it.
    setMenuOpen(open: boolean) {
        this.els.menu?.classList.toggle("is-open", open);
        if (!open) return;
        this.host.holdChrome();
        requestAnimationFrame(() => { if (this.isOpen()) this.selectedItem()?.focus({ preventScroll: true }); });
    }

    close(restoreFocus = false) {
        if (!this.isOpen()) return;
        this.setMenuOpen(false);
        if (this.host.openSettingsSection() === this.section) this.host.hideSettingsPanel();
        this.host.releaseChrome();
        if (restoreFocus) this.host.returnFocus();
    }

    contains(target: Node | null) {
        return Boolean(target && (this.els.menu?.contains(target) || this.els.button?.contains(target)));
    }

    // cycle steps to the next track in order; an optional track (subtitles)
    // has "off" as one of the stops, a required one wraps to the first.
    cycle() {
        if (!this.visible || this.tracks.length === 0) return;
        const current = this.currentTrack();
        const next = this.tracks[(current ? this.tracks.indexOf(current) : -1) + 1];
        const target = next?.id ?? (this.offLabel === null ? this.tracks[0].id : null);
        if (target !== (current?.id ?? null)) this.select(target);
    }

    // toggle switches an optional track (subtitles) between off and its default.
    toggle() {
        if (!this.visible || this.offLabel === null) return;
        const selected = this.tracks.find((track) => track.selected);
        this.select(selected ? null : this.defaultTrack()?.id ?? null);
    }

    private defaultTrack() {
        return this.tracks.find((track) => track.default) ?? this.tracks[0];
    }

    private currentTrack() {
        return this.tracks.find((track) => track.selected) ?? (this.offLabel === null ? this.defaultTrack() : undefined);
    }

    private select(id: number | null) {
        const player = this.host.trackSwitchingPlayer();
        if (!player) return;
        if (id !== null && !this.tracks.some((track) => track.id === id)) return;
        this.apply(player, id);
        // Reflect the choice immediately; the player confirms it on the next
        // state event.
        this.tracks = this.tracks.map((track) => ({ ...track, selected: track.id === id }));
        this.syncSelection();
        this.host.revealChrome();
    }

    private items() {
        return Array.from(this.els.menu?.querySelectorAll<HTMLButtonElement>("[data-track]") || []);
    }

    private selectedItem() {
        return this.items().find((item) => item.classList.contains("is-selected")) || this.items()[0] || null;
    }

    private render() {
        if (!this.els.menu) return;
        const items = this.tracks.map((track, index) => menuItemMarkup(`data-track="${track.id}"`, nativeTrackLabel(track, index)));
        if (this.offLabel !== null) items.unshift(menuItemMarkup('data-track="no"', this.offLabel));
        this.els.menu.innerHTML = `<div class="video-menu-title">${this.title}</div><div class="video-track-list" role="radiogroup" aria-label="${this.title} tracks">${items.join("")}</div>`;
    }

    private syncSelection() {
        const current = this.currentTrack();
        const key = current ? String(current.id) : "no";
        for (const item of this.items()) {
            const on = item.dataset.track === key;
            item.classList.toggle("is-selected", on);
            item.setAttribute("aria-checked", on ? "true" : "false");
            item.tabIndex = on ? 0 : -1;
        }
        const index = current ? this.tracks.indexOf(current) : -1;
        const short = current ? shortNativeTrackLabel(current, index) : this.offLabel ?? "";
        const full = current ? nativeTrackLabel(current, index) : this.offLabel ?? "";
        if (this.els.label) this.els.label.textContent = short;
        if (this.els.button) {
            const choices = this.tracks.length + (this.offLabel === null ? 0 : 1);
            this.els.button.dataset.state = current ? "on" : "off";
            this.els.button.title = `${this.title}: ${full}${choices > 1 ? ". Click to cycle" : ""}`;
            this.els.button.setAttribute("aria-label", this.els.button.title);
        }
    }
}
