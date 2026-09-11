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
    import {
        authHint,
        authPhone,
        authScreen,
        authSubmission,
        type AuthFlow,
    } from './auth-store';
    import { personalDriveSetup } from './personal-drive-store';
    import PersonalDriveSetup from './PersonalDriveSetup.svelte';
    import { installUpdate, openReleasePage } from '../../modules/updates';
    import { isVersionSkipped } from '../updates/update-model';
    import { updatePrefs, updateState } from '../updates/update-store';

    interface Props {
        onSetup: (apiId: string, apiHash: string) => void | Promise<void>;
        onPhone: (phone: string) => void | Promise<void>;
        onCode: (code: string) => void | Promise<void>;
        onPassword: (password: string) => void | Promise<void>;
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

    // A new step starts clean, while a failed request stays on the same step and
    // therefore keeps the value the user entered.
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
            if ($authScreen !== screen || appearanceOpen) return;
            const el = screen === 'setup' ? apiIdEl
                : screen === 'phone' ? phoneEl
                : screen === 'code' ? codeEl
                : screen === 'password' ? passwordEl
                : null;
            el?.focus();
        });
    });

    function submit(event: SubmitEvent, flow: AuthFlow, action: () => void | Promise<void>): void {
        event.preventDefault();
        if ($authSubmission[flow].busy) return;
        void action();
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

<!-- Appearance stays off the drive picker: someone recovering their files
     is not choosing a theme, and the popover would compete with the list. -->
{#if $authScreen && $authScreen !== 'drive'}
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
    <form
        class="auth-box auth-form"
        aria-labelledby="auth-setup-title"
        aria-busy={$authSubmission.setup.busy}
        novalidate
        onsubmit={(event) => submit(event, 'setup', () => onSetup(apiId, apiHash))}
    >
        <div class="auth-icon-box">
            <SettingsIcon size={32} strokeWidth={1.5} aria-hidden="true" />
        </div>
        <h2 id="auth-setup-title">Connect to Telegram</h2>
        <p class="auth-intro">Use API credentials created for your own Telegram account.</p>
        <p id="telegram-credentials-help" class="auth-guidance">
            Open <strong>my.telegram.org/apps</strong>, create an app, then copy its API ID and API hash.
        </p>
        <div class="auth-fields">
            <div class="auth-field">
                <label for="telegram-api-id">API ID</label>
                <input
                    bind:this={apiIdEl}
                    bind:value={apiId}
                    id="telegram-api-id"
                    name="api-id"
                    type="text"
                    inputmode="numeric"
                    autocomplete="off"
                    pattern="[0-9]*"
                    placeholder="12345678"
                    required
                    disabled={$authSubmission.setup.busy}
                    aria-invalid={$authSubmission.setup.error ? 'true' : undefined}
                    aria-describedby={$authSubmission.setup.error
                        ? 'telegram-credentials-help setup-storage-note setup-error'
                        : 'telegram-credentials-help setup-storage-note'}
                />
            </div>
            <div class="auth-field">
                <label for="telegram-api-hash">API hash</label>
                <input
                    bind:value={apiHash}
                    id="telegram-api-hash"
                    name="api-hash"
                    type="password"
                    inputmode="text"
                    autocomplete="off"
                    autocapitalize="none"
                    spellcheck="false"
                    placeholder="Your API hash"
                    required
                    disabled={$authSubmission.setup.busy}
                    aria-invalid={$authSubmission.setup.error ? 'true' : undefined}
                    aria-describedby={$authSubmission.setup.error
                        ? 'telegram-credentials-help setup-storage-note setup-error'
                        : 'telegram-credentials-help setup-storage-note'}
                />
            </div>
        </div>
        <p id="setup-storage-note" class="auth-privacy">
            Credentials and your Telegram session stay in TDrive's private app-data folder on this device. TDrive has no analytics or external tracking.
        </p>
        <p id="setup-error" class="auth-error" aria-live="polite" aria-atomic="true">
            {$authSubmission.setup.error}
        </p>
        <button class="primary-btn auth-submit" type="submit" disabled={$authSubmission.setup.busy}>
            {$authSubmission.setup.busy ? 'Saving configuration…' : 'Save configuration'}
        </button>
    </form>
{:else if $authScreen === 'phone'}
    <form
        class="auth-box auth-form"
        aria-labelledby="auth-phone-title"
        aria-busy={$authSubmission.phone.busy}
        novalidate
        onsubmit={(event) => submit(event, 'phone', () => onPhone(phone))}
    >
        <div class="auth-icon-box">
            <LockKeyholeIcon size={32} strokeWidth={1.5} aria-hidden="true" />
        </div>
        <h2 id="auth-phone-title">Sign in to Telegram</h2>
        <p class="auth-intro">Telegram will send a login code to your account.</p>
        <div class="auth-field">
            <label for="telegram-phone">Phone number</label>
            <input
                bind:this={phoneEl}
                bind:value={phone}
                id="telegram-phone"
                name="phone"
                type="tel"
                inputmode="tel"
                autocomplete="tel"
                autocapitalize="none"
                spellcheck="false"
                placeholder="+1 555 123 4567"
                required
                disabled={$authSubmission.phone.busy}
                aria-invalid={$authSubmission.phone.error ? 'true' : undefined}
                aria-describedby={$authSubmission.phone.error ? 'phone-session-note phone-error' : 'phone-session-note'}
            />
        </div>
        <p id="phone-session-note" class="auth-privacy">
            Your signed-in session is kept locally on this device and is not synced by TDrive.
        </p>
        <p id="phone-error" class="auth-error" aria-live="polite" aria-atomic="true">
            {$authSubmission.phone.error}
        </p>
        <button class="primary-btn auth-submit" type="submit" disabled={$authSubmission.phone.busy}>
            {$authSubmission.phone.busy ? 'Sending code…' : 'Send code'}
        </button>
    </form>
{:else if $authScreen === 'code'}
    <form
        class="auth-box auth-form"
        aria-labelledby="auth-code-title"
        aria-busy={$authSubmission.code.busy}
        novalidate
        onsubmit={(event) => submit(event, 'code', () => onCode(code))}
    >
        <div class="auth-icon-box">
            <MailIcon size={32} strokeWidth={1.5} aria-hidden="true" />
        </div>
        <h2 id="auth-code-title">Verify your account</h2>
        <p class="auth-intro">Enter the login code Telegram sent you.</p>
        {#if $authPhone}
            <div class="auth-helper-row">
                <span class="auth-helper-label">Sent to</span>
                <span class="auth-helper-pill">{$authPhone}</span>
                <button type="button" class="auth-link" disabled={$authSubmission.code.busy} onclick={onBackToPhone}>
                    Change
                </button>
            </div>
        {/if}
        <div class="auth-field">
            <label for="telegram-code">Login code</label>
            <input
                bind:this={codeEl}
                bind:value={code}
                id="telegram-code"
                class="code-input"
                name="login-code"
                type="text"
                inputmode="numeric"
                autocomplete="one-time-code"
                autocapitalize="none"
                spellcheck="false"
                pattern="[0-9]*"
                placeholder="12345"
                required
                disabled={$authSubmission.code.busy}
                aria-invalid={$authSubmission.code.error ? 'true' : undefined}
                aria-describedby={$authSubmission.code.error ? 'code-error' : undefined}
            />
        </div>
        <p id="code-error" class="auth-error" aria-live="polite" aria-atomic="true">
            {$authSubmission.code.error}
        </p>
        <button class="primary-btn auth-submit" type="submit" disabled={$authSubmission.code.busy}>
            {$authSubmission.code.busy ? 'Verifying…' : 'Verify'}
        </button>
    </form>
{:else if $authScreen === 'password'}
    <form
        class="auth-box auth-form"
        aria-labelledby="auth-password-title"
        aria-busy={$authSubmission.password.busy}
        novalidate
        onsubmit={(event) => submit(event, 'password', () => onPassword(password))}
    >
        <div class="auth-icon-box">
            <KeyRoundIcon size={32} strokeWidth={1.5} aria-hidden="true" />
        </div>
        <h2 id="auth-password-title">Two-step verification</h2>
        <p class="auth-intro">Enter your Telegram password to finish signing in.</p>
        {#if $authHint}
            <p id="password-hint" class="auth-caption">Hint: <span>{$authHint}</span></p>
        {/if}
        <div class="auth-field">
            <label for="telegram-password">Telegram password</label>
            <div class="input-with-action">
                <input
                    bind:this={passwordEl}
                    bind:value={password}
                    id="telegram-password"
                    name="password"
                    type={revealPassword ? 'text' : 'password'}
                    autocomplete="current-password"
                    placeholder="Your password"
                    required
                    disabled={$authSubmission.password.busy}
                    aria-invalid={$authSubmission.password.error ? 'true' : undefined}
                    aria-describedby={$authSubmission.password.error
                        ? ($authHint ? 'password-hint password-error' : 'password-error')
                        : ($authHint ? 'password-hint' : undefined)}
                />
                <button
                    class="input-action-btn"
                    type="button"
                    data-state={revealPassword ? 'visible' : 'hidden'}
                    aria-label={revealPassword ? 'Hide password' : 'Show password'}
                    title={revealPassword ? 'Hide password' : 'Show password'}
                    disabled={$authSubmission.password.busy}
                    onclick={() => { revealPassword = !revealPassword; passwordEl?.focus(); }}
                >
                    <EyeIcon class="input-action-icon icon-eye" size={20} strokeWidth={2} aria-hidden="true" />
                    <EyeOffIcon class="input-action-icon icon-eye-off" size={20} strokeWidth={2} aria-hidden="true" />
                </button>
            </div>
        </div>
        <p id="password-error" class="auth-error" aria-live="polite" aria-atomic="true">
            {$authSubmission.password.error}
        </p>
        <button class="primary-btn auth-submit" type="submit" disabled={$authSubmission.password.busy}>
            {$authSubmission.password.busy ? 'Unlocking…' : 'Unlock'}
        </button>
    </form>
{:else if $authScreen === 'drive'}
    <PersonalDriveSetup
        phase={$personalDriveSetup.phase}
        candidates={$personalDriveSetup.candidates}
        error={$personalDriveSetup.error}
        detail={$personalDriveSetup.detail}
        scan={$personalDriveSetup.scan}
        waitSeconds={$personalDriveSetup.waitSeconds}
        createRetry={$personalDriveSetup.createRetry}
        onSelect={onDriveSelect}
        onCreate={onDriveCreate}
        onRetry={onDriveRetry}
    />
{/if}

<style>
    .auth-form {
        display: flex;
        flex-direction: column;
    }

    .auth-intro {
        margin: 0 0 1rem;
    }

    .auth-guidance,
    .auth-privacy {
        margin: 0 0 1rem;
        color: var(--color-text-muted);
        font-size: 0.8rem;
        line-height: 1.5;
        text-align: left;
    }

    .auth-guidance {
        padding: 0.75rem 0;
        border-top: 1px solid var(--border);
        border-bottom: 1px solid var(--border);
    }

    .auth-guidance strong {
        color: var(--color-text-soft);
        font-weight: 600;
    }

    .auth-fields {
        display: grid;
        gap: 0.9rem;
        margin-bottom: 1rem;
    }

    .auth-field {
        text-align: left;
    }

    .auth-field > label {
        display: block;
        margin-bottom: 0.4rem;
        color: var(--color-text-soft);
        font-size: 0.82rem;
        font-weight: 600;
    }

    .auth-field input {
        margin-bottom: 0;
    }

    .auth-field input[aria-invalid='true'] {
        border-color: var(--danger);
    }

    .auth-error {
        margin: 0 0 0.8rem;
        color: var(--danger);
        font-size: 0.82rem;
        line-height: 1.4;
        text-align: left;
    }

    .auth-error:empty {
        display: none;
    }

    .auth-caption {
        margin: 0 0 0.75rem;
    }

    .auth-submit {
        margin-top: 0.15rem;
    }

    .auth-form button:disabled,
    .auth-form input:disabled {
        cursor: not-allowed;
        opacity: 0.62;
    }

    .auth-form .primary-btn:disabled:hover {
        background: var(--accent);
        transform: none;
    }
</style>
