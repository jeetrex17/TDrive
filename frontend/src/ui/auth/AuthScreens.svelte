<script lang="ts">
    import EyeIcon from '@lucide/svelte/icons/eye';
    import EyeOffIcon from '@lucide/svelte/icons/eye-off';
    import KeyRoundIcon from '@lucide/svelte/icons/key-round';
    import LockKeyholeIcon from '@lucide/svelte/icons/lock-keyhole';
    import MailIcon from '@lucide/svelte/icons/mail';
    import PaletteIcon from '@lucide/svelte/icons/palette';
    import SettingsIcon from '@lucide/svelte/icons/settings';
    import { tick } from 'svelte';
    import { eventOccurredWithin } from '../event-path';
    import AppearancePanel from '../theme/AppearancePanel.svelte';
    import { recoverThemeTransitionClick } from '../theme/theme-interaction';
    import { authCodeReset, authHint, authPhone, authScreen } from './auth-store';
    import { personalDriveSetup } from './personal-drive-store';
    import PersonalDriveSetup from './PersonalDriveSetup.svelte';
    import { installUpdate, openReleasePage } from '../../modules/updates';
    import { isVersionSkipped } from '../updates/update-model';
    import { updatePrefs, updateState } from '../updates/update-store';

    interface Props {
        onSetup: (apiId: string, apiHash: string) => void;
        onPhone: (phone: string) => void;
        onCode: (code: string) => void;
        onPassword: (password: string) => void;
        onBackToPhone: () => void;
        onDriveSelect?: (channelID: string) => void;
        onDriveCreate?: () => void;
        onDriveRetry?: () => void;
    }

    let {
        onSetup,
        onPhone,
        onCode,
        onPassword,
        onBackToPhone,
        onDriveSelect = () => undefined,
        onDriveCreate = () => undefined,
        onDriveRetry = () => undefined,
    }: Props = $props();

    // Update discoverability for users who are stuck before login (e.g. a
    // Telegram API change breaks sign-in). The updater runs independently of
    // auth, so a ready build can be installed straight from here.
    const updateFooter = $derived.by(() => {
        const s = $updateState;
        const latest = s.latest;
        if (!latest || isVersionSkipped(latest.version, $updatePrefs.skippedVersion)) return null;
        if (s.phase === 'ready') {
            return { version: latest.version, label: 'Restart to update', action: installUpdate };
        }
        if (s.phase === 'available' && s.installable) {
            return { version: latest.version, label: 'Get the update', action: openReleasePage };
        }
        return null;
    });

    let apiId = $state('');
    let apiHash = $state('');
    let phone = $state('');
    let code = $state('');
    let password = $state('');
    let revealPassword = $state(false);
    let appearanceOpen = $state(false);
    let appearanceRoot = $state<HTMLElement | null>(null);
    let appearancePopover = $state<HTMLElement | null>(null);
    let appearanceTrigger = $state<HTMLButtonElement | null>(null);

    let apiIdEl = $state<HTMLInputElement | null>(null);
    let phoneEl = $state<HTMLInputElement | null>(null);
    let codeEl = $state<HTMLInputElement | null>(null);
    let passwordEl = $state<HTMLInputElement | null>(null);

    // Reset per-screen inputs and focus the primary field on each transition,
    // matching the old per-container clear/focus behavior.
    let lastScreen: string | null = null;
    $effect(() => {
        const screen = $authScreen;
        if (!screen) appearanceOpen = false;
        if (screen === lastScreen) return;
        lastScreen = screen;
        if (screen === 'code') code = '';
        if (screen === 'password') {
            password = '';
            revealPassword = false;
        }
        void tick().then(() => {
            if ($authScreen !== screen) return;
            if (appearanceOpen) return;
            const el = screen === 'setup' ? apiIdEl
                : screen === 'phone' ? phoneEl
                : screen === 'code' ? codeEl
                : screen === 'password' ? passwordEl
                : null;
            el?.focus();
        });
    });

    // Rejected code: clear and refocus the field without a screen change.
    let lastReset = 0;
    $effect(() => {
        const nonce = $authCodeReset;
        if (nonce === lastReset) return;
        lastReset = nonce;
        code = '';
        void tick().then(() => codeEl?.focus());
    });

    function submitOnEnter(event: KeyboardEvent, action: () => void): void {
        if (event.key === 'Enter') {
            event.preventDefault();
            action();
        }
    }

    function closeAppearance(returnFocus = false): void {
        appearanceOpen = false;
        if (returnFocus) void tick().then(() => appearanceTrigger?.focus());
    }

    function toggleAppearance(): void {
        appearanceOpen = !appearanceOpen;
    }

    function onWindowKeydown(event: KeyboardEvent): void {
        if (event.key !== 'Escape' || !appearanceOpen) return;
        event.preventDefault();
        closeAppearance(true);
    }

    function onDocumentClick(event: MouseEvent): void {
        if (!appearanceOpen
            || eventOccurredWithin(event, appearanceRoot)
            || recoverThemeTransitionClick(event, appearancePopover)
            || recoverThemeTransitionClick(event, appearanceRoot)) return;
        closeAppearance();
    }
</script>

<svelte:window onkeydown={onWindowKeydown} />
<svelte:document onclickcapture={onDocumentClick} />

{#if $authScreen}
    <div class="auth-appearance-control" bind:this={appearanceRoot}>
        <button
            bind:this={appearanceTrigger}
            id="auth-appearance-trigger"
            data-theme-hit-target
            class="auth-appearance-trigger"
            type="button"
            aria-label="Customize appearance"
            aria-haspopup="dialog"
            aria-expanded={appearanceOpen}
            aria-controls="auth-appearance-popover"
            title="Customize appearance"
            onclick={toggleAppearance}
        >
            <PaletteIcon size={18} strokeWidth={2} aria-hidden="true" />
        </button>
        {#if appearanceOpen}
            <div
                bind:this={appearancePopover}
                id="auth-appearance-popover"
                class="auth-appearance-popover"
                role="dialog"
                aria-label="Appearance settings"
            >
                <AppearancePanel autofocus />
            </div>
        {/if}
    </div>
{/if}

{#if $authScreen && updateFooter}
    <div class="auth-update-footer">
        <button type="button" onclick={() => void updateFooter.action()}>
            TDrive <span class="auth-update-accent">{updateFooter.version}</span> · {updateFooter.label}
        </button>
    </div>
{/if}

{#if $authScreen === 'setup'}
    <div class="auth-box">
        <div class="auth-icon-box">
            <SettingsIcon size={32} strokeWidth={1.5} aria-hidden="true" />
        </div>
        <h2>System Setup</h2>
        <p>Configure API Credentials</p>
        <input bind:this={apiIdEl} bind:value={apiId} type="text" placeholder="App ID" />
        <input bind:value={apiHash} type="text" placeholder="App Hash" onkeydown={(e) => submitOnEnter(e, () => onSetup(apiId, apiHash))} />
        <button class="primary-btn" type="button" onclick={() => onSetup(apiId, apiHash)}>Save Configuration</button>
    </div>
{:else if $authScreen === 'phone'}
    <div class="auth-box">
        <div class="auth-icon-box">
            <LockKeyholeIcon size={32} strokeWidth={1.5} aria-hidden="true" />
        </div>
        <h2>Welcome Back</h2>
        <p>Login to your secure cloud</p>
        <input bind:this={phoneEl} bind:value={phone} type="text" placeholder="+1234567890" onkeydown={(e) => submitOnEnter(e, () => onPhone(phone))} />
        <button class="primary-btn" type="button" onclick={() => onPhone(phone)}>Send Code</button>
    </div>
{:else if $authScreen === 'code'}
    <div class="auth-box">
        <div class="auth-icon-box">
            <MailIcon size={32} strokeWidth={1.5} aria-hidden="true" />
        </div>
        <h2>Verify Identity</h2>
        <p class="auth-subtitle">Enter Telegram Code</p>
        {#if $authPhone}
            <div class="auth-helper-row">
                <span class="auth-helper-label">Sent to</span>
                <span class="auth-helper-pill">{$authPhone}</span>
                <button type="button" class="auth-link" onclick={onBackToPhone}>Change</button>
            </div>
        {/if}
        <input bind:this={codeEl} bind:value={code} class="code-input" type="text" placeholder="12345" onkeydown={(e) => submitOnEnter(e, () => onCode(code))} />
        <button class="primary-btn" type="button" onclick={() => onCode(code)}>Verify</button>
    </div>
{:else if $authScreen === 'password'}
    <div class="auth-box">
        <div class="auth-icon-box">
            <KeyRoundIcon size={32} strokeWidth={1.5} aria-hidden="true" />
        </div>
        <h2>Two-Factor Auth</h2>
        <p class="auth-subtitle">Enter your Telegram password</p>
        {#if $authHint}
            <div class="auth-caption">Hint: <span>{$authHint}</span></div>
        {/if}
        <div class="input-with-action">
            <input
                bind:this={passwordEl}
                bind:value={password}
                type={revealPassword ? 'text' : 'password'}
                placeholder="Password"
                autocomplete="current-password"
                onkeydown={(e) => submitOnEnter(e, () => onPassword(password))}
            />
            <button
                class="input-action-btn"
                type="button"
                data-state={revealPassword ? 'visible' : 'hidden'}
                aria-label={revealPassword ? 'Hide password' : 'Show password'}
                title={revealPassword ? 'Hide password' : 'Show password'}
                onclick={() => { revealPassword = !revealPassword; passwordEl?.focus(); }}
            >
                <EyeIcon class="input-action-icon icon-eye" size={20} strokeWidth={2} aria-hidden="true" />
                <EyeOffIcon class="input-action-icon icon-eye-off" size={20} strokeWidth={2} aria-hidden="true" />
            </button>
        </div>
        <button class="primary-btn" type="button" onclick={() => onPassword(password)}>Unlock</button>
    </div>
{:else if $authScreen === 'drive'}
    <PersonalDriveSetup
        phase={$personalDriveSetup.phase}
        candidates={$personalDriveSetup.candidates}
        error={$personalDriveSetup.error}
        detail={$personalDriveSetup.detail}
        createRetry={$personalDriveSetup.createRetry}
        onSelect={onDriveSelect}
        onCreate={onDriveCreate}
        onRetry={onDriveRetry}
    />
{/if}
