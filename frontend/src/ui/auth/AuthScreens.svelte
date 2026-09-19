<script lang="ts">
    import tdriveLogo from '../../assets/images/tdrive-logo.png';
    import EyeIcon from '@lucide/svelte/icons/eye';
    import EyeOffIcon from '@lucide/svelte/icons/eye-off';
    import KeyRoundIcon from '@lucide/svelte/icons/key-round';
    import LockKeyholeIcon from '@lucide/svelte/icons/lock-keyhole';
    import MailIcon from '@lucide/svelte/icons/mail';
    import SettingsIcon from '@lucide/svelte/icons/settings';
    import { tick } from 'svelte';
    import { isMobilePlatform, openExternalUrl } from '../../api';
    import { authScreen } from '../app/app-store';
    import {
        authHint,
        authPhone,
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
        onDriveRetry?: () => void | Promise<void>;
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

    const TELEGRAM_APPS_URL = 'https://my.telegram.org/apps';

    // Phones get full-bleed pages and a welcome page in front of the
    // credentials form. The welcome is local presentation only: the auth
    // state machine stays on 'setup' underneath it.
    const mobile = isMobilePlatform();
    let welcomed = $state(false);
    const step = $derived(mobile && $authScreen === 'setup' && !welcomed ? 'welcome' : $authScreen);

    // Reading the clipboard needs the async API, which older webviews and
    // insecure contexts leave out; the fields still accept a native paste.
    const canPaste = mobile
        && typeof navigator !== 'undefined'
        && typeof navigator.clipboard?.readText === 'function';

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

    let apiIdEl = $state<HTMLInputElement | null>(null);
    let apiHashEl = $state<HTMLInputElement | null>(null);
    let phoneEl = $state<HTMLInputElement | null>(null);
    let codeEl = $state<HTMLInputElement | null>(null);
    let passwordEl = $state<HTMLInputElement | null>(null);

    // A new step starts clean, while a failed request stays on the same step and
    // therefore keeps the value the user entered.
    let lastStep: string | null = null;
    $effect(() => {
        const screen = step;
        if (screen === lastStep) return;
        lastStep = screen;
        if (screen === 'code') code = '';
        if (screen === 'password') {
            password = '';
            revealPassword = false;
        }
        void tick().then(() => {
            if (step !== screen) return;
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

    // The phone keyboard's return key moves on to the hash instead of
    // submitting half a credential pair.
    function advanceTo(event: KeyboardEvent, next: HTMLInputElement | null): void {
        if (event.key !== 'Enter' || event.isComposing) return;
        event.preventDefault();
        next?.focus();
    }

    // readText must be the first thing the tap does, or iOS drops the gesture
    // that authorises it. A refused or empty clipboard leaves the field
    // focused for a native paste.
    async function paste(field: 'apiId' | 'apiHash'): Promise<void> {
        let text = '';
        try {
            text = (await navigator.clipboard.readText()).trim();
        } catch {
            // Denied by the user or the webview; nothing to paste.
        }
        if (text) {
            if (field === 'apiId') apiId = text;
            else apiHash = text;
        }
        (field === 'apiId' ? apiIdEl : apiHashEl)?.focus();
    }
</script>

{#snippet pasteButton(field: 'apiId' | 'apiHash', label: string)}
    {#if canPaste}
        <button
            class="input-action-btn input-action-text"
            type="button"
            aria-label={label}
            disabled={$authSubmission.setup.busy}
            onclick={() => void paste(field)}
        >
            Paste
        </button>
    {/if}
{/snippet}

{#if $authScreen && updateFooter}
    <div class="auth-update-footer">
        <button type="button" onclick={() => void updateFooter.action()}>
            TDrive <span class="auth-update-accent">{updateFooter.version}</span> · {updateFooter.label}
        </button>
    </div>
{/if}

{#if step === 'welcome'}
    <section class="auth-box auth-welcome" aria-labelledby="auth-welcome-title">
        <div class="auth-page-body">
            <img class="auth-mark" src={tdriveLogo} alt="" width="112" height="112" />
            <h2 id="auth-welcome-title">Your Telegram, as a drive.</h2>
            <p class="auth-intro">
                TDrive keeps your files in a private channel on your own Telegram account. There is no TDrive
                server in between, so you sign in with your own API credentials and phone number.
            </p>
        </div>
        <div class="auth-actions">
            <button class="primary-btn auth-submit" type="button" onclick={() => { welcomed = true; }}>
                Continue
            </button>
        </div>
    </section>
{:else if step === 'setup'}
    <form
        class="auth-box auth-form"
        aria-labelledby="auth-setup-title"
        aria-busy={$authSubmission.setup.busy}
        novalidate
        onsubmit={(event) => submit(event, 'setup', () => onSetup(apiId, apiHash))}
    >
        <div class="auth-page-body">
            <div class="auth-icon-box">
                <SettingsIcon size={32} strokeWidth={1.5} aria-hidden="true" />
            </div>
            <h2 id="auth-setup-title">Connect to Telegram</h2>
            {#if mobile}
                <p id="telegram-credentials-help" class="auth-intro">
                    Create an app at
                    <button
                        class="auth-inline-link"
                        type="button"
                        onclick={() => openExternalUrl(TELEGRAM_APPS_URL)}
                    >my.telegram.org/apps</button>
                    and copy its API ID and API hash here.
                </p>
            {:else}
                <p class="auth-intro">Use API credentials created for your own Telegram account.</p>
                <p id="telegram-credentials-help" class="auth-guidance">
                    Open <strong>my.telegram.org/apps</strong>, create an app, then copy its API ID and API hash.
                </p>
            {/if}
            <div class="auth-fields">
                <div class="auth-field">
                    <label for="telegram-api-id">API ID</label>
                    <div class:input-with-action={canPaste}>
                        <input
                            bind:this={apiIdEl}
                            bind:value={apiId}
                            id="telegram-api-id"
                            name="api-id"
                            type="text"
                            inputmode="numeric"
                            enterkeyhint={mobile ? 'next' : undefined}
                            autocomplete="off"
                            pattern="[0-9]*"
                            placeholder="12345678"
                            required
                            disabled={$authSubmission.setup.busy}
                            aria-invalid={$authSubmission.setup.error ? 'true' : undefined}
                            aria-describedby={$authSubmission.setup.error
                                ? 'telegram-credentials-help setup-storage-note setup-error'
                                : 'telegram-credentials-help setup-storage-note'}
                            onkeydown={mobile ? (event) => advanceTo(event, apiHashEl) : undefined}
                        />
                        {@render pasteButton('apiId', 'Paste API ID')}
                    </div>
                </div>
                <div class="auth-field">
                    <label for="telegram-api-hash">API hash</label>
                    <div class:input-with-action={canPaste}>
                        <input
                            bind:this={apiHashEl}
                            bind:value={apiHash}
                            id="telegram-api-hash"
                            name="api-hash"
                            type="password"
                            inputmode="text"
                            enterkeyhint={mobile ? 'done' : undefined}
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
                        {@render pasteButton('apiHash', 'Paste API hash')}
                    </div>
                </div>
            </div>
            {#if mobile}
                <p id="setup-storage-note" class="auth-privacy">
                    TDrive ships without API credentials because Telegram limits keys shared across many
                    installs. Yours stay in the app's private storage on this phone.
                </p>
            {:else}
                <p id="setup-storage-note" class="auth-privacy">
                    Credentials and your Telegram session stay in TDrive's private app-data folder on this device. TDrive has no analytics or external tracking.
                </p>
            {/if}
            <p id="setup-error" class="auth-error" aria-live="polite" aria-atomic="true">
                {$authSubmission.setup.error}
            </p>
        </div>
        <div class="auth-actions">
            <button class="primary-btn auth-submit" type="submit" disabled={$authSubmission.setup.busy}>
                {$authSubmission.setup.busy ? 'Saving configuration…' : 'Save configuration'}
            </button>
        </div>
    </form>
{:else if step === 'phone'}
    <form
        class="auth-box auth-form"
        aria-labelledby="auth-phone-title"
        aria-busy={$authSubmission.phone.busy}
        novalidate
        onsubmit={(event) => submit(event, 'phone', () => onPhone(phone))}
    >
        <div class="auth-page-body">
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
                    enterkeyhint={mobile ? 'send' : undefined}
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
        </div>
        <div class="auth-actions">
            <button class="primary-btn auth-submit" type="submit" disabled={$authSubmission.phone.busy}>
                {$authSubmission.phone.busy ? 'Sending code…' : 'Send code'}
            </button>
        </div>
    </form>
{:else if step === 'code'}
    <form
        class="auth-box auth-form"
        aria-labelledby="auth-code-title"
        aria-busy={$authSubmission.code.busy}
        novalidate
        onsubmit={(event) => submit(event, 'code', () => onCode(code))}
    >
        <div class="auth-page-body">
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
                    enterkeyhint={mobile ? 'go' : undefined}
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
        </div>
        <div class="auth-actions">
            <button class="primary-btn auth-submit" type="submit" disabled={$authSubmission.code.busy}>
                {$authSubmission.code.busy ? 'Verifying…' : 'Verify'}
            </button>
        </div>
    </form>
{:else if step === 'password'}
    <form
        class="auth-box auth-form"
        aria-labelledby="auth-password-title"
        aria-busy={$authSubmission.password.busy}
        novalidate
        onsubmit={(event) => submit(event, 'password', () => onPassword(password))}
    >
        <div class="auth-page-body">
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
                        enterkeyhint={mobile ? 'go' : undefined}
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
        </div>
        <div class="auth-actions">
            <button class="primary-btn auth-submit" type="submit" disabled={$authSubmission.password.busy}>
                {$authSubmission.password.busy ? 'Unlocking…' : 'Unlock'}
            </button>
        </div>
    </form>
{:else if step === 'drive'}
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

    /* Phone pages. The wrapper, inputs and action bar are restyled in
       auth.css; this block only re-tunes the component's own type and
       spacing to the mobile scale (15 body, 13 fine print, 16 inputs). */
    :global(html.mobile) .auth-intro {
        margin: 0 0 1.5rem;
        font-size: 0.9375rem;
        line-height: 1.5;
    }

    :global(html.mobile) .auth-welcome .auth-intro {
        margin-bottom: 0;
    }

    /* Optically centred, not mathematically. A block placed at exact centre on
       a tall phone screen reads as having sunk, because the eye gives the upper
       half more weight; lifting it by a tenth of the viewport puts it where it
       looks centred. The padding is what does the lifting, so the block still
       falls back to a true centre if the text grows enough to fill the space. */
    :global(html.mobile) .auth-welcome .auth-page-body {
        padding-bottom: 10vh;
    }

    /* Centred and large: on the one screen with no content to compete with,
       the logo is the thing that says whose app this is. */
    :global(html.mobile) .auth-mark {
        display: block;
        width: 112px;
        height: auto;
        margin: 0 auto 1.75rem;
    }

    :global(html.mobile) .auth-welcome h2 {
        font-size: 1.75rem;
    }

    :global(html.mobile) .auth-inline-link {
        padding: 0;
        color: var(--accent);
        font: inherit;
        font-weight: 600;
        background: none;
        border: 0;
        border-radius: var(--radius-xs);
        cursor: pointer;
    }

    :global(html.mobile) .auth-inline-link:focus-visible {
        outline: none;
        box-shadow: var(--focus-ring);
    }

    /* Fine print follows the inline error, so an error sits right under
       the field it belongs to. */
    :global(html.mobile) .auth-privacy {
        order: 1;
        margin: 0 0 1rem;
        font-size: 0.8125rem;
        color: var(--color-text-muted);
    }

    :global(html.mobile) .auth-fields {
        gap: 1rem;
        margin-bottom: 1.25rem;
    }

    :global(html.mobile) .auth-field > label {
        margin-bottom: 0.375rem;
        font-size: 0.8125rem;
    }

    :global(html.mobile) .auth-field:not(.auth-fields .auth-field) {
        margin-bottom: 1.25rem;
    }

    :global(html.mobile) .auth-error {
        margin: -0.25rem 0 1rem;
        font-size: 0.875rem;
        line-height: 1.45;
    }

    :global(html.mobile) .auth-caption {
        margin: -0.75rem 0 1rem;
    }

    :global(html.mobile) .auth-submit {
        margin-top: 0;
    }

    :global(html.mobile) .auth-form button:disabled,
    :global(html.mobile) .auth-form input:disabled {
        cursor: default;
    }

    /* The state matrix wants the loading state inside the button. The label
       already reads "Sending code", so the ring is decoration for sighted
       users and stays out of the accessibility tree. */
    :global(html.mobile) .auth-form[aria-busy='true'] .auth-submit::before {
        display: inline-block;
        width: 16px;
        height: 16px;
        margin-right: 8px;
        vertical-align: -3px;
        content: '';
        border: 2px solid currentColor;
        border-top-color: transparent;
        border-radius: 50%;
        animation: auth-submit-spin 720ms linear infinite;
    }

    @keyframes auth-submit-spin {
        to { transform: rotate(360deg); }
    }
</style>
