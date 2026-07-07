// Authentication flows for TDrive frontend.
//
// The four auth screens (setup, phone, code, 2FA password) are rendered by
// AuthScreens.svelte from the auth store; this module owns the orchestration:
// the login state machine, InitDrive/dashboard bring-up, and the Telegram
// event stream that advances screens.

import { state } from '../state';
import {
    CheckSystemStatus, SaveSetup,
    LoginPhoneNumber, SumbitCode, SumbitPassword,
    CheckLoginStatus, InitDrive, MyUserID, SyncChannel,
} from '../../wailsjs/go/main/App';
import { renderBreadcrumb } from './navigation';
import { loadChannels } from './channels';
import { loadEncryptionStatus } from './encryption';
import { loadSelfUser } from './profile-menu';
import { notify, dismissNotification } from './notifications';
import AuthScreens from '../ui/auth/AuthScreens.svelte';
import { authCodeReset, authHint, authPhone, authScreen } from '../ui/auth/auth-store';
import { mountSvelte, type SvelteMountHandle } from '../ui/mount';

let authScreensHandle: SvelteMountHandle<Record<string, unknown>> | null = null;

function successScreen(): HTMLElement | null {
    return document.getElementById('success-screen');
}

export function hideAllScreens() {
    authScreen.set(null);
    const dashboard = successScreen();
    if (dashboard) dashboard.style.display = 'none';
}

export function showAuthWrapper() {
    const authWrapper = document.getElementById('auth-wrapper');
    if (authWrapper) authWrapper.style.display = 'flex';

    const dashboard = successScreen();
    if (dashboard) dashboard.style.display = 'none';
}

async function initDriveWithRetry(maxAttempts = 3) {
    let lastError = "Error: Init failed";

    for (let attempt = 1; attempt <= maxAttempts; attempt++) {
        notify({
            id: 'init-drive',
            level: 'info',
            title: maxAttempts > 1 ? `Setting up your drive… (${attempt}/${maxAttempts})` : 'Setting up your drive…',
            sticky: true,
            spinner: true,
        });

        try {
            const initRes = await InitDrive();

            if (typeof initRes === "string" && initRes.startsWith("Error:")) {
                lastError = initRes;
                console.error("InitDrive error:", initRes);

                if (initRes.includes("could not save config")) {
                    break;
                }
            } else {
                return { ok: true, message: initRes };
            }
        } catch (err) {
            lastError = "Error: " + ((err as any)?.message || String(err));
            console.error("InitDrive failed:", err);
        }

        if (attempt < maxAttempts) {
            await new Promise((resolve) => setTimeout(resolve, 700 * attempt));
        }
    }

    return { ok: false, error: lastError };
}

export async function showDashboard() {
    const authWrapper = document.getElementById("auth-wrapper");
    if (authWrapper) authWrapper.style.display = "none";

    authScreen.set(null);
    successScreen()!.style.display = "flex";
    state.currentFolderId = "";
    state.folderPath = [];
    renderBreadcrumb();

    const initResult = await initDriveWithRetry(3);
    dismissNotification('init-drive');

    if (!initResult.ok) {
        notify({
            level: 'error',
            title: 'Could not initialize your drive',
            body: initResult.error || 'Check logs/console and try again.',
        });
        return;
    }

    // Load drive list (personal + any joined shared) before the first
    // refresh, so the sidebar populates and folder-control gating runs
    // based on the active drive. triggerRefresh syncs from Telegram first
    // — important for users coming back to a drive that's seen new
    // activity since they last had the app open.
    await loadChannels();

    // Resolve self user id once. Owner-only actions on shared drives
    // depend on this; if it fails (e.g. offline), default-deny by
    // leaving state.myUserID = 0.
    try {
        const id = await MyUserID();
        state.myUserID = Number(id) || 0;
    } catch (err) {
        console.warn('MyUserID failed:', err);
        state.myUserID = 0;
    }

    // Pull Telegram metadata before reading encryption state. On a fresh
    // reinstall this is what restores the wrapped master key into SQLite.
    await window.triggerRefresh();
    const personal = state.channels.find((c) => c?.kind === 'personal');
    if (personal && Number(personal.id) !== Number(state.activeChannel?.id || 0)) {
        try {
            await SyncChannel(Number(personal.id));
        } catch (err) {
            console.warn('Personal sync before encryption status failed:', err);
        }
    }

    // Refresh the personal-drive encryption snapshot so the upload dialog
    // can decide between first-time setup and password entry.
    await loadEncryptionStatus();

    // Hydrate the profile menu (display name, photo). Failure is non-fatal —
    // the avatar falls back to a blank circle.
    loadSelfUser();
}

export async function checkStatusAndShowScreen() {
    try {
        // Step A: Check Setup
        const status = await CheckSystemStatus();

        if (status === "NEEDS_SETUP") {
            showAuthWrapper();
            authScreen.set('setup');
            return;
        }

        // Step B: Check Login
        const isLoggedIn = await CheckLoginStatus();
        if (isLoggedIn) {
            showDashboard();
        } else {
            showAuthWrapper();
            authScreen.set('phone');
        }
    } catch (err) {
        console.error("Startup Crash:", err);
        notify({
            level: 'error',
            title: 'Startup error',
            body: String(err) + " — did you restart `wails dev`?",
        });
    }
}

// --- screen submit handlers (wired into AuthScreens) ---

function submitSetup(apiIdRaw: string, apiHash: string): void {
    const id = parseInt(apiIdRaw, 10);
    const hash = (apiHash || '').trim();
    if (!id || !hash) {
        notify({ level: 'warning', title: 'Enter both API ID and hash' });
        return;
    }
    SaveSetup(id, hash).then((res) => {
        if (res === "Success") location.reload();
        else notify({ level: 'error', title: 'Setup failed', body: String(res) });
    });
}

function submitPhone(phoneRaw: string): void {
    const phone = (phoneRaw || '').trim();
    if (!phone) {
        notify({ level: 'warning', title: 'Enter your phone number' });
        return;
    }

    LoginPhoneNumber(phone).then(() => {
        showAuthWrapper();
        authPhone.set(phone);
        authScreen.set('code');
    }).catch((err) => {
        notify({ level: 'error', title: 'Could not start login', body: String(err) });
    });
}

function submitCode(codeRaw: string): void {
    const code = (codeRaw || '').trim();
    if (!code) {
        notify({ level: 'warning', title: 'Enter the code from Telegram' });
        return;
    }
    SumbitCode(code).catch((err) => {
        notify({ level: 'error', title: 'Could not submit code', body: String(err) });
    });
}

function submitPassword(password: string): void {
    SumbitPassword(password).catch((err) => {
        notify({ level: 'error', title: 'Could not submit password', body: String(err) });
    });
}

function backToPhone(): void {
    showAuthWrapper();
    authScreen.set('phone');
}

export function setupAuthWindowBindings() {
    const host = document.getElementById('auth-wrapper');
    if (host && !authScreensHandle) {
        authScreensHandle = mountSvelte(AuthScreens, {
            target: host,
            props: {
                onSetup: submitSetup,
                onPhone: submitPhone,
                onCode: submitCode,
                onPassword: submitPassword,
                onBackToPhone: backToPhone,
            },
        });
    }

    // The login-flow listeners below need the Wails runtime. Boot waits for it
    // (see waitForWailsRuntime in main.ts), but guard here too so a genuinely
    // missing runtime degrades to the startup error toast instead of throwing
    // and blanking the whole window. Mirrors the window.runtime?.EventsOn guards
    // in transfers.ts.
    if (!window.runtime?.EventsOn) return;

    window.runtime.EventsOn("login-success", () => showDashboard());

    window.runtime.EventsOn("login-password-required", () => {
        showAuthWrapper();
        authHint.set('');
        authScreen.set('password');
    });

    window.runtime.EventsOn("login-error", (msg: any) => {
        notify({ level: 'error', title: 'Login failed', body: String(msg || 'Try again.') });
    });

    // Wrong code: the backend keeps the login attempt alive and waits for a new
    // code, so stay on the code screen (AuthScreens clears + refocuses it) and
    // surface the error.
    window.runtime.EventsOn("login-code-invalid", () => {
        showAuthWrapper();
        authScreen.set('code');
        authCodeReset.update((n) => n + 1);
        notify({ level: 'error', title: 'Wrong code', body: 'That code was incorrect — try again.' });
    });

    window.runtime.EventsOn("gothint", (hint: any) => {
        const text = (hint ?? "").toString().trim();
        const normalized = text.replace(/^(hint\s*:?[\s\u00A0]*)+/i, "").trim();
        if (!normalized || normalized.toLowerCase().includes("no hint")) {
            authHint.set('');
            return;
        }
        authHint.set(normalized);
    });
}
