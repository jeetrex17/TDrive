<script lang="ts">
    import CircleUserIcon from '@lucide/svelte/icons/circle-user';
    import CloudUploadIcon from '@lucide/svelte/icons/cloud-upload';
    import DownloadIcon from '@lucide/svelte/icons/download';
    import HardDriveIcon from '@lucide/svelte/icons/hard-drive';
    import LockKeyholeIcon from '@lucide/svelte/icons/lock-keyhole';
    import PaletteIcon from '@lucide/svelte/icons/palette';
    import XIcon from '@lucide/svelte/icons/x';
    import LanguagesIcon from '@lucide/svelte/icons/languages';
    import { tick } from 'svelte';
    import ModalShell from '../modals/ModalShell.svelte';
    import SwitchRow from '../SwitchRow.svelte';
    import Avatar from '../chrome/Avatar.svelte';
    import { encryptionEntryVisible, profileLoaded, profileUser } from '../chrome/profile-store';
    import PhotoBackupPanel from '../gallery/PhotoBackupPanel.svelte';
    import MountLetterSetting from '../mount/MountLetterSetting.svelte';
    import { mountLetter } from '../mount/mount-letter-store';
    import AppearancePanel from '../theme/AppearancePanel.svelte';
    import { recoverThemeTransitionClick } from '../theme/theme-interaction';
    import UpdatesPanel from '../updates/UpdatesPanel.svelte';
    import { updateBadge } from '../updates/update-store';
    import { checkForUpdates } from '../../modules/updates';
    import { encryptUploads, setEncryptUploads } from '../../modules/upload-preferences';
    import { closeSettings, openSettings, settingsView, showSettingsSection, type SettingsSection } from './settings-store';
    import { locale, localePreference, setLocalePreference, type LocalePreference } from '../i18n/locale-store';
    import { translate, type MessageKey } from '../i18n/messages';

    interface Props {
        // False on phones, which update through their app stores.
        updaterAvailable?: boolean;
        onEncryptionSettings: () => void;
        onLogout: () => void;
    }

    let { updaterAvailable = true, onEncryptionSettings, onLogout }: Props = $props();

    const SECTIONS: ReadonlyArray<{ id: SettingsSection; labelKey: MessageKey; icon: typeof PaletteIcon }> = [
        { id: 'account', labelKey: 'account', icon: CircleUserIcon },
        { id: 'appearance', labelKey: 'appearance', icon: PaletteIcon },
        { id: 'language', labelKey: 'language', icon: LanguagesIcon },
        { id: 'backup', labelKey: 'photoBackup', icon: CloudUploadIcon },
        { id: 'encryption', labelKey: 'encryption', icon: LockKeyholeIcon },
        { id: 'mount', labelKey: 'mount', icon: HardDriveIcon },
        { id: 'updates', labelKey: 'updates', icon: DownloadIcon },
    ];

    // Mount only has a setting where drives have letters: Windows.
    const sections = $derived(SECTIONS.map((entry) => ({ ...entry, label: translate(entry.labelKey, $locale) })).filter((entry) => {
        if (entry.id === 'updates') return updaterAvailable;
        if (entry.id === 'mount') return $mountLetter.available;
        return true;
    }));
    const current = $derived(sections.find((entry) => entry.id === $settingsView.section) ?? sections[0]);
    const displayName = $derived(!$profileLoaded ? translate('loadingAccount', $locale) : $profileUser?.displayName || translate('telegramAccount', $locale));
    const handle = $derived(($profileUser?.username || '').trim());

    // The update section asks for a fresh check each time it is shown, as the
    // menu's Software Update view did.
    let lastSection: SettingsSection | null = null;
    $effect(() => {
        const section = $settingsView.open ? current.id : null;
        if (section === lastSection) return;
        lastSection = section;
        if (section === 'updates') void checkForUpdates();
    });

    $effect(() => {
        if ($settingsView.open) void mountLetter.load();
    });

    async function select(section: SettingsSection): Promise<void> {
        showSettingsSection(section);
        await tick();
        document.getElementById('settings-section-title')?.focus();
    }

    function moveSectionFocus(event: KeyboardEvent, index: number): void {
        const next = event.key === 'ArrowDown' ? index + 1 : event.key === 'ArrowUp' ? index - 1 : null;
        if (next === null) return;
        event.preventDefault();
        const target = sections[(next + sections.length) % sections.length];
        showSettingsSection(target.id);
        void tick().then(() => document.getElementById(`settings-nav-${target.id}`)?.focus());
    }

    // Leaving Settings for a task of its own: the task's dialog takes the
    // screen instead of stacking a second modal on this one.
    function handOff(action: () => void): void {
        closeSettings();
        action();
    }

    // A palette click during the theme's own transition lands on <html>, not
    // on the swatch the reader can still see; replay it onto the swatch.
    function onDocumentClick(event: MouseEvent): void {
        if (!$settingsView.open) return;
        recoverThemeTransitionClick(event, document.querySelector<HTMLElement>('.settings-card'));
    }

    // ⌘, (Ctrl+, elsewhere) is where every desktop app keeps its settings.
    function onWindowKeydown(event: KeyboardEvent): void {
        if (event.key !== ',' || !(event.metaKey || event.ctrlKey) || event.altKey || event.shiftKey) return;
        event.preventDefault();
        openSettings();
    }
</script>

<svelte:window onkeydown={onWindowKeydown} />
<svelte:document onclickcapture={onDocumentClick} />

{#snippet noHeader()}{/snippet}

<ModalShell
    hostId="settings-modal"
    open={$settingsView.open}
    titleId="settings-title"
    cardClass="settings-card"
    presentation="dialog"
    initialFocus={`#settings-nav-${current.id}`}
    restoreFocus="#profile-trigger"
    header={noHeader}
    onClose={closeSettings}
>
    <div class="settings-layout">
        <nav class="settings-nav" aria-labelledby="settings-title">
            <h2 id="settings-title" class="settings-nav-title">{translate('settings', $locale)}</h2>
            {#each sections as entry, index (entry.id)}
                {@const Icon = entry.icon}
                <button
                    id={`settings-nav-${entry.id}`}
                    class="settings-nav-item"
                    class:active={entry.id === current.id}
                    type="button"
                    aria-current={entry.id === current.id ? 'page' : undefined}
                    onclick={() => void select(entry.id)}
                    onkeydown={(event) => moveSectionFocus(event, index)}
                >
                    <Icon size={16} strokeWidth={2} aria-hidden="true" />
                    <span>{entry.label}</span>
                    {#if entry.id === 'updates' && $updateBadge === 'ready'}
                        <span class="settings-nav-dot" aria-label="Update ready"></span>
                    {/if}
                </button>
            {/each}
        </nav>

        <section class="settings-content" aria-labelledby="settings-section-title">
            <header class="settings-content-head">
                <h3 id="settings-section-title" tabindex="-1">{current.label}</h3>
                <button class="viewer-icon-btn settings-close" type="button" aria-label={translate('closeSettings', $locale)} title={translate('closeSettings', $locale)} onclick={closeSettings}>
                    <XIcon aria-hidden="true" />
                </button>
            </header>

            <div class="settings-body">
                {#if current.id === 'account'}
                    <div class="settings-group">
                        <div class="settings-identity">
                            <Avatar user={$profileUser} />
                            <div>
                                <div class="settings-identity-name">{displayName}</div>
                                {#if handle}<div class="settings-identity-handle">@{handle}</div>{/if}
                            </div>
                        </div>
                    </div>
                    <div class="settings-group">
                        <div class="settings-row">
                            <div class="settings-row-copy">
                                <div class="settings-row-title">{translate('logOut', $locale)}</div>
                                <div class="settings-row-description">{translate('logOutDescription', $locale)}</div>
                            </div>
                            <button class="secondary-btn settings-danger" type="button" onclick={() => handOff(onLogout)}>{translate('logOut', $locale)}…</button>
                        </div>
                    </div>
                {:else if current.id === 'appearance'}
                    <AppearancePanel layout="settings" />
                {:else if current.id === 'backup'}
                    <PhotoBackupPanel page />
                {:else if current.id === 'encryption'}
                    <div class="settings-group">
                        <SwitchRow
                            title={translate('encryptUploads', $locale)}
                            description={translate('encryptUploadsDescription', $locale)}
                            checked={$encryptUploads === true}
                            onchange={(checked) => setEncryptUploads(checked)}
                        />
                    </div>
                    <div class="settings-group">
                        <div class="settings-row">
                            <div class="settings-row-copy">
                                <div class="settings-row-title">{translate('passwordAndDeviceUnlock', $locale)}</div>
                                <div class="settings-row-description">
                                    {$encryptionEntryVisible
                                        ? translate('changeEncryptionPassword', $locale)
                                        : translate('setEncryptionPassword', $locale)}
                                </div>
                            </div>
                            {#if $encryptionEntryVisible}
                                <button class="secondary-btn" type="button" onclick={() => handOff(onEncryptionSettings)}>{translate('manage', $locale)}</button>
                            {/if}
                        </div>
                    </div>
                {:else if current.id === 'language'}
                    <div class="settings-group">
                        <div class="settings-row">
                            <div class="settings-row-copy">
                                <div class="settings-row-title">{translate('language', $locale)}</div>
                                <div class="settings-row-description">{translate('languageDescription', $locale)}</div>
                            </div>
                            <select id="settings-language" value={$localePreference} onchange={(event) => setLocalePreference(event.currentTarget.value as LocalePreference)} aria-label={translate('language', $locale)}>
                                <option value="system">{translate('systemDefault', $locale)}</option>
                                <option value="en">{translate('english', $locale)}</option>
                                <option value="zh-CN">{translate('simplifiedChinese', $locale)}</option>
                                <option value="zh-TW">{translate('traditionalChinese', $locale)}</option>
                            </select>
                        </div>
                    </div>
                {:else if current.id === 'mount'}
                    <div class="settings-group">
                        <MountLetterSetting />
                    </div>
                {:else if current.id === 'updates'}
                    <UpdatesPanel embedded />
                {/if}
            </div>
        </section>
    </div>
</ModalShell>

<style>
    :global(.settings-card.modal-card) {
        width: min(880px, calc(100vw - 48px));
        max-width: none;
        height: min(620px, calc(100vh - 48px));
        padding: 0;
        overflow: hidden;
    }

    .settings-layout {
        display: grid;
        grid-template-columns: 200px minmax(0, 1fr);
        height: 100%;
    }

    /* The section list sits on the window canvas, like the app's own sidebar;
       the open section is the raised surface beside it. */
    .settings-nav {
        display: flex;
        flex-direction: column;
        gap: 2px;
        padding: var(--space-4) var(--space-2);
        background: var(--color-canvas);
        border-right: 1px solid var(--color-border-soft);
    }

    .settings-nav-title {
        margin: 0 0 var(--space-2);
        padding: 0 var(--space-2);
        color: var(--color-text);
        font-size: var(--type-base);
        font-weight: var(--weight-semibold);
    }

    .settings-nav-item {
        display: flex;
        align-items: center;
        gap: 10px;
        min-height: 32px;
        padding: 6px 8px;
        border: 0;
        border-radius: var(--radius-sm);
        background: transparent;
        color: var(--color-text-muted);
        font: inherit;
        font-size: var(--type-sm);
        font-weight: var(--weight-medium);
        text-align: left;
        cursor: pointer;
        transition: background var(--motion-fast) var(--ease-standard), color var(--motion-fast) var(--ease-standard);
    }

    @media (hover: hover) {
        .settings-nav-item:hover { background: var(--overlay-neutral-1); color: var(--color-text); }
    }

    .settings-nav-item.active { background: var(--color-current-bg); color: var(--color-current-text); }
    .settings-nav-item:focus-visible { outline: none; box-shadow: var(--focus-ring); }

    .settings-nav-dot {
        width: 6px;
        height: 6px;
        margin-left: auto;
        border-radius: 50%;
        background: var(--color-accent);
    }

    .settings-content {
        display: flex;
        flex-direction: column;
        min-width: 0;
        min-height: 0;
    }

    .settings-content-head {
        display: flex;
        align-items: center;
        justify-content: space-between;
        flex: 0 0 auto;
        min-height: 56px;
        padding: 0 var(--space-3) 0 var(--space-6);
    }

    .settings-content-head h3 {
        margin: 0;
        color: var(--color-text);
        font-size: var(--type-lg);
        font-weight: var(--weight-semibold);
        letter-spacing: -0.01em;
        outline: none;
    }

    .settings-close { color: var(--color-text-muted); }

    .settings-body {
        flex: 1 1 auto;
        min-height: 0;
        overflow-y: auto;
        padding: 0 var(--space-6) var(--space-6);
        display: grid;
        align-content: start;
        gap: var(--space-4);
    }

    /* A group is the frame a set of related rows shares, as the phone's
       Account tab draws them. */
    .settings-group {
        background: var(--color-surface-1);
        border: 1px solid var(--color-border-soft);
        border-radius: var(--radius-md);
    }

    .settings-row {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: var(--space-4);
        padding: 12px 16px;
    }

    .settings-row-copy { min-width: 0; }
    .settings-row-title { color: var(--color-text); font-size: var(--type-sm); font-weight: var(--weight-medium); }
    .settings-row-description { margin-top: 2px; color: var(--color-text-muted); font-size: var(--type-xs); line-height: 1.45; }
    .settings-row .secondary-btn { flex: 0 0 auto; min-height: var(--control-md); padding: 0 var(--space-3); }
    .settings-danger { color: var(--color-danger); }

    .settings-identity {
        display: flex;
        align-items: center;
        gap: var(--space-3);
        padding: 14px 16px;
    }

    .settings-identity-name { color: var(--color-text); font-size: var(--type-base); font-weight: var(--weight-semibold); }
    .settings-identity-handle { margin-top: 2px; color: var(--color-text-muted); font-size: var(--type-sm); }
</style>
