/**
 * The settings dock: the panel that slides between the topbar and the controls,
 * and the popovers that live in it — picture mode, audio, subtitles and speed.
 *
 * All four are one surface because only one may be on screen at a time and each
 * one changes the panel's height, which the native video rect and the HTML
 * picture box are both measured against. Owning them together is what lets a tab
 * click swap sections in place instead of closing the panel and reopening it,
 * which reads as the popover dismissing itself.
 *
 * The panel's own elements are resolved at use time rather than cached: the panel
 * is rendered by Svelte and may be mounted after the controller is installed.
 */

import { isMobilePlatform } from '../../api';
import type { PictureMode, PlaybackPreferences } from './playback-preferences';
import type { NativeMediaTrack } from './media-tracks';
import type { PlayerAdapter, PlayerState, TrackSwitching } from './player-adapters';
import type { VideoChromeController } from './video-chrome';
import { byID, type VideoDOM } from './video-dom';
import { SETTINGS_SECTIONS, type SettingsSection } from './video-menu-dom';
import { SpeedMenuController } from './video-speed-menu';
import { TrackPicker } from './video-track-picker';

const PICTURE_MODES: PictureMode[] = ['fit', 'fill', 'original', '16:9', '4:3'];
const PICTURE_LABELS: Record<PictureMode, string> = { fit: 'Fit', fill: 'Fill', original: 'Original', '16:9': '16:9', '4:3': '4:3' };
// A phone's control row is a few hundred pixels wide and every pill on it
// competes for the same space. "Original" alone is wide enough to push the row
// onto a second line, so the pill says the short form and the description the
// button announces stays the full one.
const PICTURE_PILL_LABELS: Record<PictureMode, string> = { ...PICTURE_LABELS, original: 'Orig' };

// Bitmap subtitle streams are pictures, not text, so the appearance controls in
// this section cannot restyle them and the panel says so instead.
const BITMAP_SUBTITLE_CODECS = new Set([
    'hdmv_pgs_subtitle', 'pgs', 'dvd_subtitle', 'dvdsub', 'dvb_subtitle', 'dvbsub', 'xsub',
]);

const ANCHOR_ID = 'video-picture-button';
const PANEL_ID = 'video-settings-panel';

export interface VideoSettingsDockContext {
    dom: VideoDOM;
    chrome: VideoChromeController;
    adapter(): PlayerAdapter | null;
    state(): PlayerState;
    /** The active player when it is one the track pills can drive. */
    trackPlayer(): TrackSwitching | null;
    preferences(): PlaybackPreferences;
    updatePreferences(value: PlaybackPreferences): void;
    /** The playlist shares the panel slot, so opening this closes that. */
    hidePlaylist(): void;
    /**
     * Dismisses whatever the player currently has open. It goes through the
     * coordinator because the playlist is a peer of this dock, not part of it.
     */
    closeOpenMenu(): void;
    syncPanelGeometry(): void;
    syncViewportInsets(): void;
    scheduleNativeResize(): void;
    applyPicture(): void;
}

export class VideoSettingsDock {
    readonly audio: TrackPicker;
    readonly subtitles: TrackPicker;
    readonly speed: SpeedMenuController;
    /** Built once: these two are iterated on every dismissal check. */
    private readonly pickers: readonly TrackPicker[];

    private section: SettingsSection | null = null;
    private returnFocus: HTMLElement | null = null;
    private unbindDocumentClick: (() => void) | null = null;

    constructor(private readonly ctx: VideoSettingsDockContext) {
        const pickerContext = {
            chrome: ctx.chrome,
            player: () => ctx.trackPlayer(),
            isSectionActive: (section: SettingsSection) => this.section === section,
            hideSection: () => this.hidePanel(),
            focusAnchor: () => this.focusAnchor(),
        };
        this.audio = new TrackPicker('Audio', null, (player, id) => {
            if (id !== null) player.setAudioTrack(id);
        }, ctx.dom.audioPicker, pickerContext);
        this.subtitles = new TrackPicker(
            'Subtitles',
            'Off',
            (player, id) => player.setSubtitleTrack(id),
            ctx.dom.subtitlePicker,
            pickerContext,
        );
        this.pickers = [this.audio, this.subtitles];
        this.speed = new SpeedMenuController({
            dom: ctx.dom,
            chrome: ctx.chrome,
            adapter: () => ctx.adapter(),
            state: () => ctx.state(),
            closeOthers: () => this.closeAll('speed'),
            showSection: () => this.showPanel('speed'),
            isSectionActive: () => this.section === 'speed',
            hideSection: () => this.hidePanel(),
            focusAnchor: () => this.focusAnchor(),
        });
    }

    get openSection(): SettingsSection | null {
        return this.section;
    }

    get panel(): HTMLElement | null {
        return byID(PANEL_ID);
    }

    /** Whether any of this dock's popovers is holding the chrome open. */
    hasOpenPopover(): boolean {
        return this.section !== null || this.speed.isOpen() || this.pickers.some((picker) => picker.isOpen());
    }

    bind(): void {
        this.speed.render();
        this.speed.bind();
        this.bindPanel();
        this.unbindDocumentClick = this.bindDocumentDismiss();
    }

    destroy(): void {
        this.unbindDocumentClick?.();
        this.unbindDocumentClick = null;
    }

    showPanel(section: SettingsSection): void {
        const panel = this.panel;
        if (!panel) return;
        this.returnFocus = byID(ANCHOR_ID);
        this.ctx.hidePlaylist();
        this.section = section;
        panel.hidden = false;
        panel.setAttribute('aria-hidden', 'false');
        panel.inert = false;
        this.ctx.dom.modal?.classList.add('has-video-settings');
        byID(ANCHOR_ID)?.setAttribute('aria-expanded', 'true');
        const picture = byID('video-picture-settings');
        const appearance = byID('video-subtitle-settings');
        if (picture) picture.hidden = section !== 'picture';
        if (appearance) appearance.hidden = section !== 'subtitle';
        for (const button of panel.querySelectorAll<HTMLElement>('[data-settings-section]')) {
            button.setAttribute('aria-pressed', button.dataset.settingsSection === section ? 'true' : 'false');
        }
        this.ctx.syncPanelGeometry();
        this.ctx.chrome.clearTimer();
        this.ctx.chrome.reveal();
        // The panel animates in, so the geometry it changed is only final on the
        // next frame.
        requestAnimationFrame(() => {
            this.ctx.syncPanelGeometry();
            this.ctx.scheduleNativeResize();
            this.ctx.applyPicture();
        });
    }

    hidePanel(restoreFocus = false): void {
        this.section = null;
        const panel = this.panel;
        const focusInside = Boolean(panel?.contains(document.activeElement));
        if (panel) {
            panel.hidden = true;
            panel.setAttribute('aria-hidden', 'true');
            panel.inert = true;
        }
        this.ctx.dom.modal?.classList.remove('has-video-settings');
        byID(ANCHOR_ID)?.setAttribute('aria-expanded', 'false');
        // Focus must never be left inside an inert panel.
        if ((restoreFocus || focusInside) && this.returnFocus?.isConnected) this.returnFocus.focus({ preventScroll: true });
        this.ctx.syncViewportInsets();
        this.ctx.scheduleNativeResize();
        this.ctx.applyPicture();
    }

    /**
     * Swaps which section the open panel shows. Routing through the pickers
     * instead would close the panel first and reopen it.
     */
    showSection(section: SettingsSection): void {
        this.speed.closeInPlace();
        for (const picker of this.pickers) picker.setMenuOpen(false);
        this.showPanel(section);
        if (section === 'audio') this.audio.setMenuOpen(true);
        else if (section === 'subtitle') this.subtitles.setMenuOpen(true);
        else if (section === 'speed') this.speed.openInPlace();
    }

    /** Closes every popover except the one about to open. */
    closeAll(except: TrackPicker | 'speed' | null = null): void {
        if (this.section !== null) this.hidePanel();
        if (except !== 'speed') this.speed.close();
        for (const picker of this.pickers) {
            if (picker !== except) picker.close();
        }
    }

    /** Dismisses whatever is open and hands focus back to the anchor. */
    closeOpen(): void {
        this.speed.close(true);
        for (const picker of this.pickers) picker.close(true);
        this.hidePanel(true);
    }

    syncAspectButton(): void {
        const button = byID('video-aspect-button');
        if (!button) return;
        const mode = this.ctx.preferences().pictureMode;
        const label = PICTURE_LABELS[mode];
        button.textContent = isMobilePlatform() ? PICTURE_PILL_LABELS[mode] : label;
        button.title = `Video fit: ${label}. Click to cycle`;
        button.setAttribute('aria-label', button.title);
    }

    /**
     * Keeps available audio and subtitle tracks discoverable even when there is
     * only one. A pill appearing changes the control row's height, so the native
     * viewport is re-measured when it does.
     */
    syncTracks(tracks: NativeMediaTrack[]): void {
        const available = this.ctx.trackPlayer() !== null;
        const audio = tracks.filter((track) => track.type === 'audio');
        const subtitles = tracks.filter((track) => track.type === 'subtitle');
        const selectedCodec = subtitles.find((track) => track.selected)?.codec?.toLowerCase() ?? '';
        const formatNote = byID('video-subtitle-format-note');
        if (formatNote) formatNote.hidden = !available || !BITMAP_SUBTITLE_CODECS.has(selectedCodec);
        const audioChanged = this.audio.update(available ? audio : [], available && audio.length > 0);
        const subtitleChanged = this.subtitles.update(available ? subtitles : [], available && subtitles.length > 0);
        if (audioChanged || subtitleChanged) this.ctx.scheduleNativeResize();
    }

    /** Returns focus to the control the dock is opened from. */
    focusAnchor(): void {
        byID(ANCHOR_ID)?.focus({ preventScroll: true });
    }

    private bindPanel(): void {
        byID(ANCHOR_ID)?.addEventListener('click', (event) => {
            event.stopPropagation();
            const wasOpen = this.section !== null;
            this.closeAll();
            if (wasOpen) return;
            this.showPanel('picture');
            this.panel?.querySelector<HTMLButtonElement>('[data-picture-mode]')?.focus();
        });
        byID('video-aspect-button')?.addEventListener('click', (event) => {
            event.stopPropagation();
            const preferences = this.ctx.preferences();
            const next = PICTURE_MODES[(PICTURE_MODES.indexOf(preferences.pictureMode) + 1) % PICTURE_MODES.length];
            this.ctx.updatePreferences({ ...preferences, pictureMode: next });
            this.ctx.chrome.reveal();
        });
        this.syncAspectButton();
        byID('video-settings-close')?.addEventListener('click', () => this.ctx.closeOpenMenu());
        const panel = this.panel;
        panel?.addEventListener('click', (event) => {
            const button = (event.target as HTMLElement).closest<HTMLElement>('[data-settings-section]');
            if (!button) return;
            // A tab click is navigation within the panel, so the control the
            // panel was opened from stays the one focus returns to.
            const returnFocus = this.returnFocus;
            const requested = button.dataset.settingsSection;
            this.showSection(SETTINGS_SECTIONS.find((value) => value === requested) ?? 'picture');
            this.returnFocus = returnFocus;
        });
        panel?.addEventListener('keydown', (event) => {
            if (event.key !== 'Escape') return;
            event.preventDefault();
            event.stopPropagation();
            this.ctx.closeOpenMenu();
        });
    }

    /** A click anywhere else dismisses a popover, but never through the panel itself. */
    private bindDocumentDismiss(): () => void {
        const onDocumentClick = (event: MouseEvent) => {
            const target = event.target as Node | null;
            if (target && this.panel?.contains(target)) return;
            if (this.speed.isOpen() && !this.speed.contains(target)) this.speed.close();
            for (const picker of this.pickers) {
                if (picker.isOpen() && !picker.contains(target)) picker.close();
            }
        };
        document.addEventListener('click', onDocumentClick);
        return () => document.removeEventListener('click', onDocumentClick);
    }
}
