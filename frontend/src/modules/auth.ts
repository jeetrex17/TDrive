// Authentication flows for TDrive frontend.
//
// The auth and personal-drive setup screens are rendered by
// AuthScreens.svelte from the auth store; this module owns the orchestration:
// the login state machine, explicit drive recovery, dashboard bring-up, and the Telegram
// event stream that advances screens.

import { get } from 'svelte/store';
import { state } from '../state';
import type { PersonalDriveCandidate, PersonalDriveSetup } from '../types';
import {
    checkSystemStatus,
    saveSetup,
    loginPhoneNumber,
    submitCode as submitLoginCode,
    submitPassword as submitLoginPassword,
    checkLoginStatus,
    preparePersonalDrive,
    discoverPersonalDrives as fetchPersonalDrives,
    selectPersonalDrive as selectPersonalDriveApi,
    createPersonalDrive as createPersonalDriveApi,
    getMyUserId,
    syncChannel,
    onRuntimeEvent,
} from '../api';
import { renderBreadcrumb } from './navigation';
import { loadChannels } from './channels';
import { loadEncryptionStatus } from './encryption';
import { loadSelfUser } from './profile-menu';
import { notify } from './notifications';
import { appActions } from './app-actions';
import { humanizeBackendError } from './errors';
import AuthScreens from '../ui/auth/AuthScreens.svelte';
import {
    authHint,
    authPhone,
    authScreen,
    authSubmission,
    beginAuthSubmission,
    failAuthSubmission,
    finishAuthSubmission,
    resetAuthSubmissions,
    type AuthFlow,
} from '../ui/auth/auth-store';
import { parseDriveScanProgress, personalDriveSetup } from '../ui/auth/personal-drive-store';
import { mountSvelte, type SvelteMountHandle } from '../ui/mount';

let authScreensHandle: SvelteMountHandle<Record<string, unknown>> | null = null;
let personalDriveFlowVersion = 0;


function subscribeAuthEvent<TArgs extends unknown[]>(eventName: string, callback: (...data: TArgs) => void): void {
    onRuntimeEvent<TArgs>(eventName, callback);
}

function startPersonalDriveFlow(): number {
    personalDriveFlowVersion += 1;
    return personalDriveFlowVersion;
}

function isCurrentPersonalDriveFlow(version: number): boolean {
    return version === personalDriveFlowVersion;
}

// Wails rejects bindings with a plain string; keep whatever text we get so
// the user sees the real cause instead of a guess.
function errorText(err: unknown): string {
    if (err instanceof Error) return err.message;
    return String(err ?? '').trim();
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
        const id = await getMyUserId();
        if (!isCurrentPersonalDriveFlow(flowVersion)) return;
        state.myUserID = Number(id) || 0;
    } catch (err) {
        if (!isCurrentPersonalDriveFlow(flowVersion)) return;
        console.warn('getMyUserId failed:', err);
        state.myUserID = 0;
    }

    // Pull Telegram metadata before reading encryption state. On a fresh
    // reinstall this is what restores the wrapped master key into SQLite.
    try {
        await appActions().triggerRefresh();
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
            await syncChannel(Number(personal.id));
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

function showDriveSetupScreen(): void {
    showAuthWrapper();
    authScreen.set('drive');
    personalDriveSetup.loading();
}

async function discoverPersonalDrives(flowVersion: number): Promise<void> {
    let candidates: PersonalDriveCandidate[];
    try {
        candidates = await fetchPersonalDrives();
    } catch (err) {
        if (!isCurrentPersonalDriveFlow(flowVersion)) return;
        console.error('Personal drive discovery failed:', err);
        personalDriveSetup.discoveryError('Could not look up your Telegram channels.', errorText(err));
        return;
    }
    if (!isCurrentPersonalDriveFlow(flowVersion)) return;
    personalDriveSetup.showCandidates(candidates);
}

// Activates the saved drive without touching the screen: the common case
// goes straight to the dashboard. Only when the user actually has to choose
// does the drive picker appear, and only then does discovery hit Telegram.
export async function preparePersonalDriveAndContinue(): Promise<void> {
    const flowVersion = startPersonalDriveFlow();

    let setup: PersonalDriveSetup;
    try {
        setup = await preparePersonalDrive();
    } catch (err) {
        if (!isCurrentPersonalDriveFlow(flowVersion)) return;
        console.error('Personal drive preparation failed:', err);
        showDriveSetupScreen();
        personalDriveSetup.discoveryError('Could not open your saved drive.', errorText(err));
        return;
    }
    if (!isCurrentPersonalDriveFlow(flowVersion)) return;

    if (setup.status === 'ready') {
        await showDashboardForFlow(flowVersion);
        return;
    }
    showDriveSetupScreen();
    if (setup.status === 'selection_required') {
        await discoverPersonalDrives(flowVersion);
        return;
    }
    personalDriveSetup.discoveryError(
        'TDrive could not prepare your personal drive.',
        `Unexpected setup status "${setup.status}".`,
    );
}

export async function selectPersonalDrive(channelID: string): Promise<void> {
    const flowVersion = startPersonalDriveFlow();
    if (!/^[1-9]\d*$/.test(channelID)) {
        personalDriveSetup.recoveryError('Could not recover that channel. Choose a channel from the list and try again.');
        return;
    }
    personalDriveSetup.recovering({ createRetry: false });
    try {
        await selectPersonalDriveApi(channelID);
    } catch (err) {
        if (!isCurrentPersonalDriveFlow(flowVersion)) return;
        console.error('Personal drive recovery failed:', err);
        personalDriveSetup.recoveryError(
            'Could not recover this channel. Nothing was changed on Telegram.',
            { detail: errorText(err) },
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
        await createPersonalDriveApi();
    } catch (err) {
        if (!isCurrentPersonalDriveFlow(flowVersion)) return;
        console.error('Personal drive creation failed:', err);
        personalDriveSetup.recoveryError(
            'Could not finish TDrive setup. Retry continues the previous attempt without creating a duplicate channel.',
            { detail: errorText(err), createRetry: true },
        );
        return;
    }
    if (!isCurrentPersonalDriveFlow(flowVersion)) return;
    await showDashboardForFlow(flowVersion);
}

export async function checkStatusAndShowScreen() {
    resetAuthSubmissions();
    try {
        // Step A: Check Setup
        const status = await checkSystemStatus();

        if (status === "NEEDS_SETUP") {
            showAuthWrapper();
            authScreen.set('setup');
            return;
        }

        // Step B: Check Login
        const isLoggedIn = await checkLoginStatus();
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
            body: humanizeBackendError(err),
        });
    }
}

// --- screen submit handlers (wired into AuthScreens) ---

function currentAuthFlow(): AuthFlow | null {
    const screen = get(authScreen);
    return screen === 'setup' || screen === 'phone' || screen === 'code' || screen === 'password'
        ? screen
        : null;
}

async function submitSetup(apiIdRaw: string, apiHashRaw: string): Promise<void> {
    if (!beginAuthSubmission('setup')) return;

    const apiIdText = apiIdRaw.trim();
    const apiHash = apiHashRaw.trim();
    const apiId = Number(apiIdText);
    if (!/^\d+$/.test(apiIdText) || !Number.isSafeInteger(apiId) || apiId <= 0) {
        failAuthSubmission('setup', 'Enter the numeric API ID from my.telegram.org/apps.');
        return;
    }
    if (!apiHash) {
        failAuthSubmission('setup', 'Enter the API hash from my.telegram.org/apps.');
        return;
    }

    try {
        const result = await saveSetup(apiId, apiHash);
        if (result !== 'Success') {
            failAuthSubmission('setup', humanizeBackendError(result));
            return;
        }
        finishAuthSubmission('setup');
        location.reload();
    } catch (err) {
        failAuthSubmission('setup', humanizeBackendError(err));
    }
}

async function submitPhone(phoneRaw: string): Promise<void> {
    if (!beginAuthSubmission('phone')) return;

    const phone = phoneRaw.trim();
    if (!phone) {
        failAuthSubmission('phone', 'Enter the phone number for your Telegram account.');
        return;
    }

    try {
        await loginPhoneNumber(phone);
        // A very fast Telegram failure can arrive before the Wails promise
        // resolves. Do not advance over the inline error in that case.
        if (get(authSubmission).phone.error) return;
        finishAuthSubmission('phone');
        finishAuthSubmission('code');
        showAuthWrapper();
        authPhone.set(phone);
        authScreen.set('code');
    } catch (err) {
        failAuthSubmission('phone', humanizeBackendError(err));
    }
}

async function submitCode(codeRaw: string): Promise<void> {
    if (!beginAuthSubmission('code')) return;

    const code = codeRaw.trim();
    if (!code) {
        failAuthSubmission('code', 'Enter the login code Telegram sent you.');
        return;
    }

    try {
        await submitLoginCode(code);
        // Keep this flow busy until Telegram emits success, password-required,
        // code-invalid, or login-error. The bridge call only queues the code.
    } catch (err) {
        failAuthSubmission('code', humanizeBackendError(err));
    }
}

async function submitPassword(passwordRaw: string): Promise<void> {
    if (!beginAuthSubmission('password')) return;

    const password = passwordRaw.trim();
    if (!password) {
        failAuthSubmission('password', 'Enter your Telegram password.');
        return;
    }

    try {
        await submitLoginPassword(password);
        // As with the code, the terminal result arrives over the event stream.
    } catch (err) {
        failAuthSubmission('password', humanizeBackendError(err));
    }
}

function backToPhone(): void {
    finishAuthSubmission('code');
    finishAuthSubmission('phone');
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


    subscribeAuthEvent("login-success", () => {
        resetAuthSubmissions();
        void preparePersonalDriveAndContinue();
    });

    // History-scan progress. Fires for routine syncs too; the store ignores
    // anything that arrives outside an on-screen recovery.
    subscribeAuthEvent<[unknown]>("drive_scan_progress", (payload) => {
        const update = parseDriveScanProgress(payload);
        if (update) personalDriveSetup.scanProgress(update);
    });

    subscribeAuthEvent("login-password-required", () => {
        finishAuthSubmission('code');
        finishAuthSubmission('password');
        showAuthWrapper();
        authHint.set('');
        authScreen.set('password');
    });

    subscribeAuthEvent<[unknown]>("login-error", (msg) => {
        const message = humanizeBackendError(msg || 'Try again.');
        const flow = currentAuthFlow();
        if (flow) {
            showAuthWrapper();
            failAuthSubmission(flow, message);
            return;
        }
        notify({ level: 'error', title: 'Login failed', body: message });
    });

    // The backend keeps this login attempt alive. Keep the entered code visible
    // for context and let the user correct it without requesting another code.
    subscribeAuthEvent("login-code-invalid", () => {
        showAuthWrapper();
        authScreen.set('code');
        failAuthSubmission('code', 'That code was incorrect. Check it and try again.');
    });

    subscribeAuthEvent<[unknown]>("gothint", (hint) => {
        const text = (hint ?? "").toString().trim();
        const normalized = text.replace(/^(hint\s*:?[\s\u00A0]*)+/i, "").trim();
        if (!normalized || normalized.toLowerCase().includes("no hint")) {
            authHint.set('');
            return;
        }
        authHint.set(normalized);
    });
}
