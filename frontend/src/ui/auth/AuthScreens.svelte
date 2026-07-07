<script lang="ts">
    import { tick } from 'svelte';
    import { authCodeReset, authHint, authPhone, authScreen } from './auth-store';

    interface Props {
        onSetup: (apiId: string, apiHash: string) => void;
        onPhone: (phone: string) => void;
        onCode: (code: string) => void;
        onPassword: (password: string) => void;
        onBackToPhone: () => void;
    }

    let { onSetup, onPhone, onCode, onPassword, onBackToPhone }: Props = $props();

    let apiId = $state('');
    let apiHash = $state('');
    let phone = $state('');
    let code = $state('');
    let password = $state('');
    let revealPassword = $state(false);

    let apiIdEl = $state<HTMLInputElement | null>(null);
    let phoneEl = $state<HTMLInputElement | null>(null);
    let codeEl = $state<HTMLInputElement | null>(null);
    let passwordEl = $state<HTMLInputElement | null>(null);

    // Reset per-screen inputs and focus the primary field on each transition,
    // matching the old per-container clear/focus behavior.
    let lastScreen: string | null = null;
    $effect(() => {
        const screen = $authScreen;
        if (screen === lastScreen) return;
        lastScreen = screen;
        if (screen === 'code') code = '';
        if (screen === 'password') {
            password = '';
            revealPassword = false;
        }
        void tick().then(() => {
            if ($authScreen !== screen) return;
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
</script>

{#if $authScreen === 'setup'}
    <div class="auth-box">
        <div class="auth-icon-box">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z"/><path d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"/></svg>
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
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z"/></svg>
        </div>
        <h2>Welcome Back</h2>
        <p>Login to your secure cloud</p>
        <input bind:this={phoneEl} bind:value={phone} type="text" placeholder="+1234567890" onkeydown={(e) => submitOnEnter(e, () => onPhone(phone))} />
        <button class="primary-btn" type="button" onclick={() => onPhone(phone)}>Send Code</button>
    </div>
{:else if $authScreen === 'code'}
    <div class="auth-box">
        <div class="auth-icon-box">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M3 8l7.89 5.26a2 2 0 002.22 0L21 8M5 19h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z"/></svg>
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
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.743 5.743L11.536 16.464a.5.5 0 00-.496.568l.1 1.15a.5.5 0 01-.037.24l-1.352 3.863a.5.5 0 01-.47.315H7.5a.5.5 0 01-.5-.5v-2.204a.5.5 0 01.146-.354l3.298-3.297A6 6 0 0115 7z"/></svg>
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
                <svg class="input-action-icon icon-eye" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M2.458 12C3.732 7.943 7.523 5 12 5c4.477 0 8.268 2.943 9.542 7-1.274 4.057-5.065 7-9.542 7-4.477 0-8.268-2.943-9.542-7z"/>
                    <path stroke-linecap="round" stroke-linejoin="round" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"/>
                </svg>
                <svg class="input-action-icon icon-eye-off" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M3 3l18 18"/>
                    <path stroke-linecap="round" stroke-linejoin="round" d="M10.477 10.48a3 3 0 004.243 4.243"/>
                    <path stroke-linecap="round" stroke-linejoin="round" d="M9.88 5.09A10.48 10.48 0 0112 5c4.477 0 8.268 2.943 9.542 7a10.53 10.53 0 01-2.36 3.95"/>
                    <path stroke-linecap="round" stroke-linejoin="round" d="M6.53 6.53A10.52 10.52 0 002.458 12c1.274 4.057 5.065 7 9.542 7 1.11 0 2.18-.18 3.19-.51"/>
                </svg>
            </button>
        </div>
        <button class="primary-btn" type="button" onclick={() => onPassword(password)}>Unlock</button>
    </div>
{/if}
