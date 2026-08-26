// Authentication flows for TDrive frontend.
//
// The auth and personal-drive setup screens are rendered by
// AuthScreens.svelte from the auth store; this module owns the orchestration:
// the login state machine, explicit drive recovery, dashboard bring-up, and the Telegram
// event stream that advances screens.

import { state } from '../state';
import {
    CheckSystemStatus, SaveSetup,
    LoginPhoneNumber, SumbitCode, SumbitPassword,
    CheckLoginStatus, PreparePersonalDrive, SelectPersonalDrive,
    CreatePersonalDrive, MyUserID, SyncChannel,
} from '../../wailsjs/go/main/App';
import { renderBreadcrumb } from './navigation';
import { loadChannels } from './channels';
import { loadEncryptionStatus } from './encryption';
import { loadSelfUser } from './profile-menu';
import { notify } from './notifications';
import AuthScreens from '../ui/auth/AuthScreens.svelte';
import { authCodeReset, authHint, authPhone, authScreen } from '../ui/auth/auth-store';
import { personalDriveSetup } from '../ui/auth/personal-drive-store';
import { mountSvelte, type SvelteMountHandle } from '../ui/mount';

let authScreensHandle: SvelteMountHandle<Record<string, unknown>> | null = null;
let personalDriveFlowVersion = 0;

function startPersonalDriveFlow(): number {
    personalDriveFlowVersion += 1;
    return personalDriveFlowVersion;
}

function isCurrentPersonalDriveFlow(version: number): boolean {
    return version === personalDriveFlowVersion;
}

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

async function showDashboardForFlow(flowVersion: number): Promise<void> {
    if (!isCurrentPersonalDriveFlow(flowVersion)) return;

    const authWrapper = document.getElementById("auth-wrapper");
    if (authWrapper) authWrapper.style.display = "none";

    authScreen.set(null);
    const dashboard = successScreen();
    if (dashboard) dashboard.style.display = "flex";
    state.currentFolderId = "";
    state.folderPath = [];
    renderBreadcrumb();

    // Load drive list (personal + any joined shared) before the first
    // refresh, so the sidebar populates and folder-control gating runs
    // based on the active drive. triggerRefresh syncs from Telegram first
    // — important for users coming back to a drive that's seen new
    // activity since they last had the app open.
    try {
        await loadChannels();
    } catch (err) {
        if (!isCurrentPersonalDriveFlow(flowVersion)) return;
        console.error('Drive list load failed:', err);
        notify({
            level: 'error',
            title: 'Could not load your drive',
            body: 'Your TDrive is configured, but its local view could not be opened. Try refreshing.',
        });
        return;
    }
    if (!isCurrentPersonalDriveFlow(flowVersion)) return;

    // Resolve self user id once. Owner-only actions on shared drives
    // depend on this; if it fails (e.g. offline), default-deny by
    // leaving state.myUserID = 0.
    try {
        const id = await MyUserID();
        if (!isCurrentPersonalDriveFlow(flowVersion)) return;
        state.myUserID = Number(id) || 0;
    } catch (err) {
        if (!isCurrentPersonalDriveFlow(flowVersion)) return;
        console.warn('MyUserID failed:', err);
        state.myUserID = 0;
    }

    // Pull Telegram metadata before reading encryption state. On a fresh
    // reinstall this is what restores the wrapped master key into SQLite.
    try {
        await window.triggerRefresh();
    } catch (err) {
        if (!isCurrentPersonalDriveFlow(flowVersion)) return;
        console.warn('Initial drive refresh failed:', err);
        notify({
            level: 'warning',
            title: 'Drive refresh is unavailable',
            body: 'Showing the local view. Check your connection and refresh again.',
        });
    }
    if (!isCurrentPersonalDriveFlow(flowVersion)) return;
    const personal = state.channels.find((c) => c?.kind === 'personal');
    if (personal && Number(personal.id) !== Number(state.activeChannel?.id || 0)) {
        try {
            await SyncChannel(Number(personal.id));
        } catch (err) {
            if (!isCurrentPersonalDriveFlow(flowVersion)) return;
            console.warn('Personal sync before encryption status failed:', err);
        }
        if (!isCurrentPersonalDriveFlow(flowVersion)) return;
    }

    // Refresh the personal-drive encryption snapshot so the upload dialog
    // can decide between first-time setup and password entry.
    try {
        await loadEncryptionStatus();
    } catch (err) {
        if (!isCurrentPersonalDriveFlow(flowVersion)) return;
        console.warn('Encryption status load failed:', err);
        notify({
            level: 'warning',
            title: 'Encryption status is unavailable',
            body: 'Uploads stay unavailable until the drive status can be refreshed.',
        });
    }
    if (!isCurrentPersonalDriveFlow(flowVersion)) return;

    // Hydrate the profile menu (display name, photo). Failure is non-fatal —
    // the avatar falls back to a blank circle.
    loadSelfUser();
}

export async function showDashboard(): Promise<void> {
    await showDashboardForFlow(startPersonalDriveFlow());
}

export async function preparePersonalDriveAndContinue(): Promise<void> {
    const flowVersion = startPersonalDriveFlow();
    showAuthWrapper();
    authScreen.set('drive');
    personalDriveSetup.loading();

    let setup;
    try {
        setup = await PreparePersonalDrive();
    } catch (err) {
        if (!isCurrentPersonalDriveFlow(flowVersion)) return;
        console.error('Personal drive discovery failed:', err);
        personalDriveSetup.discoveryError(
            'Could not look up your Telegram channels. Check your connection and try again.',
        );
        return;
    }
    if (!isCurrentPersonalDriveFlow(flowVersion)) return;

    if (setup?.status === 'ready') {
        await showDashboardForFlow(flowVersion);
        return;
    }
    if (setup?.status === 'selection_required') {
        personalDriveSetup.showCandidates(Array.isArray(setup.candidates) ? setup.candidates : []);
        return;
    }

    personalDriveSetup.discoveryError('TDrive could not prepare your personal drive. Please try again.');
}

export async function selectPersonalDrive(channelID: string): Promise<void> {
    const flowVersion = startPersonalDriveFlow();
    if (!/^[1-9]\d*$/.test(channelID)) {
        personalDriveSetup.recoveryError('Could not recover that channel. Choose a channel from the list and try again.');
        return;
    }
    personalDriveSetup.recovering({ createRetry: false });
    try {
        await SelectPersonalDrive(channelID);
    } catch (err) {
        if (!isCurrentPersonalDriveFlow(flowVersion)) return;
        console.error('Personal drive recovery failed:', err);
        personalDriveSetup.recoveryError(
            'Could not recover this channel. Nothing was changed on Telegram; please try again.',
        );
        return;
    }
    if (!isCurrentPersonalDriveFlow(flowVersion)) return;
    await showDashboardForFlow(flowVersion);
}

export async function createPersonalDrive(): Promise<void> {
    const flowVersion = startPersonalDriveFlow();
    personalDriveSetup.recovering();
    try {
        await CreatePersonalDrive();
    } catch (err) {
        if (!isCurrentPersonalDriveFlow(flowVersion)) return;
        console.error('Personal drive creation failed:', err);
        personalDriveSetup.recoveryError(
            'Could not finish TDrive setup. Retry will continue the previous attempt without creating a duplicate channel.',
            true,
        );
        return;
    }
    if (!isCurrentPersonalDriveFlow(flowVersion)) return;
    await showDashboardForFlow(flowVersion);
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
            await preparePersonalDriveAndContinue();
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
                onDriveSelect: (channelID: string) => { void selectPersonalDrive(channelID); },
                onDriveCreate: () => { void createPersonalDrive(); },
                onDriveRetry: () => { void preparePersonalDriveAndContinue(); },
            },
        });
    }

    // The login-flow listeners below need the Wails runtime. Boot waits for it
    // (see waitForWailsRuntime in main.ts), but guard here too so a genuinely
    // missing runtime degrades to the startup error toast instead of throwing
    // and blanking the whole window. Mirrors the window.runtime?.EventsOn guards
    // in transfers.ts.
    if (!window.runtime?.EventsOn) return;

    window.runtime.EventsOn("login-success", () => { void preparePersonalDriveAndContinue(); });

    window.runtime.EventsOn("login-password-required", () => {
        showAuthWrapper();
        authHint.set('');
        authScreen.set('password');
    });

    window.runtime.EventsOn("login-error", (msg: unknown) => {
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

    window.runtime.EventsOn("gothint", (hint: unknown) => {
        const text = (hint ?? "").toString().trim();
        const normalized = text.replace(/^(hint\s*:?[\s\u00A0]*)+/i, "").trim();
        if (!normalized || normalized.toLowerCase().includes("no hint")) {
            authHint.set('');
            return;
        }
        authHint.set(normalized);
    });
}
