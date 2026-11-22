<script lang="ts">
    import CountryCodePicker from './CountryCodePicker.svelte';
    import { normalizePhoneNumber, countryForInternationalNumber, nationalNumberForCountryChange } from './phone-number';
    import type { CountryCode } from 'libphonenumber-js/min';
    import tdriveLogo from '../../assets/images/tdrive-logo.png';
    import EyeIcon from '@lucide/svelte/icons/eye';
    import EyeOffIcon from '@lucide/svelte/icons/eye-off';
    import { tick } from 'svelte';
    import { isMobilePlatform, openExternalUrl } from '../../api';
    import { authScreen } from '../app/app-store';
    import {
        failAuthSubmission,
        authHint,
        authPhone,
        authSubmission,
        type AuthFlow,
    } from './auth-store';
    import { pushSheet } from '../modals/sheet-stack';
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

    // Phone BACK steps back through sign-in the way the on-screen links do,
    // instead of falling through to the host and leaving the app.
    $effect(() => {
        if (!mobile) return;
        const back = step === 'code' && !$authSubmission.code.busy ? onBackToPhone
            : step === 'setup' ? () => { welcomed = false; }
            : null;
        if (!back) return;
        const sheet = pushSheet(back);
        return () => sheet.release();
    });

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
    let country = $state<CountryCode | undefined>();

    function updatePhone(event: Event): void {
        phone = (event.currentTarget as HTMLInputElement).value;
        if (phone.trim().startsWith('+')) country = countryForInternationalNumber(phone);
    }

    function selectCountry(next: CountryCode): void {
        phone = nationalNumberForCountryChange(phone);
        country = next;
    }

    function sendPhone(): void | Promise<void> {
        const result = normalizePhoneNumber(phone, country);
        if ('error' in result) {
            failAuthSubmission('phone', result.error);
            phoneEl?.focus();
            return;
        }
        return onPhone(result.number);
    }
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

</script>

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
            <img class="auth-step-mark" src={tdriveLogo} alt="" width="66" height="42" />
            <h2 id="auth-setup-title">Connect to Telegram</h2>
            <p id="telegram-credentials-help" class="auth-intro">
                Create an app at
                <button
                    class="auth-inline-link"
                    type="button"
                    onclick={() => openExternalUrl(TELEGRAM_APPS_URL)}
                >my.telegram.org/apps</button>
                and copy its API ID and API hash here.
            </p>
            <div class="auth-fields">
                <div class="auth-field">
                    <label for="telegram-api-id">API ID</label>
                    <div>
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
                    </div>
                </div>
                <div class="auth-field">
                    <label for="telegram-api-hash">API hash</label>
                    <div>
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
        onsubmit={(event) => submit(event, 'phone', sendPhone)}
    >
        <div class="auth-page-body">
            <img class="auth-step-mark" src={tdriveLogo} alt="" width="66" height="42" />
            <h2 id="auth-phone-title">Sign in with Telegram</h2>
            <p class="auth-intro">Enter the phone number on your Telegram account. Telegram will send a login code to your app.</p>
            <div class="auth-field">
                <label for="telegram-phone">Phone number</label>
                <!-- One field, as people read a phone number: the country code
                     leads it and the number follows, inside a single frame. -->
                <div class="phone-entry" class:is-invalid={Boolean($authSubmission.phone.error)}>
                    <CountryCodePicker {country} disabled={$authSubmission.phone.busy} onSelect={selectCountry} />
                    <input
                        bind:this={phoneEl}
                        value={phone}
                        oninput={updatePhone}
                        id="telegram-phone"
                        name="phone"
                        type="tel"
                        inputmode="tel"
                        enterkeyhint={mobile ? 'send' : undefined}
                        autocomplete="tel"
                        autocapitalize="none"
                        spellcheck="false"
                        placeholder={country ? "Phone number" : "+1 202 555 0123"}
                        required
                        disabled={$authSubmission.phone.busy}
                        aria-invalid={$authSubmission.phone.error ? 'true' : undefined}
                        aria-describedby={$authSubmission.phone.error ? 'phone-session-note phone-error' : 'phone-session-note'}
                    />
                </div>
            </div>
            <p id="phone-session-note" class="auth-privacy">
                Your session stays on this device. There is no TDrive server.
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
            <img class="auth-step-mark" src={tdriveLogo} alt="" width="66" height="42" />
            <h2 id="auth-code-title">Enter the login code</h2>
            <p class="auth-intro">Telegram sends it to your Telegram app. Check there first.</p>
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
            <img class="auth-step-mark" src={tdriveLogo} alt="" width="66" height="42" />
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
    /* The frame is the field: the code button and the number sit inside one
       border and share one focus ring, so they read as a single phone number. */
    .phone-entry {
        display: flex;
        align-items: stretch;
        min-height: 40px;
        margin-bottom: 0.75rem;
        background: var(--color-surface-1);
        border: 1px solid var(--color-border-soft);
        border-radius: var(--radius-md);
        transition: border-color var(--motion-fast) var(--ease-standard), box-shadow var(--motion-fast) var(--ease-standard);
    }
    .phone-entry:focus-within { border-color: var(--color-accent); box-shadow: var(--focus-ring); }
    .phone-entry.is-invalid { border-color: var(--danger); }
    .phone-entry :global(.country-code-trigger) {
        flex: 0 0 auto;
        width: auto;
        min-height: 0;
        padding: 0 10px 0 12px;
        border: 0;
        border-right: 1px solid var(--color-border-soft);
        border-radius: var(--radius-md) 0 0 var(--radius-md);
        background: transparent;
        font-variant-numeric: tabular-nums;
    }
    .phone-entry :global(.country-code-trigger:focus-visible) { box-shadow: none; }
    .phone-entry input,
    .phone-entry input:focus {
        flex: 1 1 auto;
        min-width: 0;
        min-height: 0;
        margin: 0;
        background: transparent;
        border: 0;
        box-shadow: none;
        font-variant-numeric: tabular-nums;
    }
    :global(html.mobile) .phone-entry { min-height: 48px; }

    .auth-form {
        display: flex;
        flex-direction: column;
    }

    .auth-intro {
        margin: 0 0 1rem;
    }

    .auth-privacy {
        margin: 0 0 1rem;
        color: var(--color-text-muted);
        font-size: var(--type-sm);
        line-height: 1.5;
        text-align: left;
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
        font-size: var(--type-sm);
        font-weight: var(--weight-semibold);
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
        font-size: var(--type-sm);
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
        background: var(--color-action);
        transform: none;
    }

    /* Phone pages. The wrapper, inputs and action bar are restyled in
       auth.css; this block only re-tunes the component's own type and
       spacing to the mobile scale (15 body, 13 fine print, 16 inputs). */
    :global(html.mobile) .auth-intro {
        margin: 0 0 1.5rem;
        font-size: var(--mobile-type-body);
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
        font-size: var(--type-3xl);
    }

    .auth-inline-link {
        padding: 0;
        color: var(--accent);
        font: inherit;
        font-weight: var(--weight-semibold);
        background: none;
        border: 0;
        border-radius: var(--radius-xs);
        cursor: pointer;
    }

    .auth-inline-link:focus-visible {
        outline: none;
        box-shadow: var(--focus-ring);
    }

    /* Fine print follows the inline error, so an error sits right under
       the field it belongs to. */
    :global(html.mobile) .auth-privacy {
        order: 1;
        margin: 0 0 1rem;
        font-size: var(--type-sm);
        color: var(--color-text-muted);
    }

    :global(html.mobile) .auth-fields {
        gap: 1rem;
        margin-bottom: 1.25rem;
    }

    :global(html.mobile) .auth-field > label {
        margin-bottom: 0.375rem;
        font-size: var(--type-sm);
    }

    :global(html.mobile) .auth-field:not(.auth-fields .auth-field) {
        margin-bottom: 1.25rem;
    }


    :global(html.mobile) .auth-error {
        margin: -0.25rem 0 1rem;
        font-size: var(--type-base);
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
