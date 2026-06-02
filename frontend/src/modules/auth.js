// Authentication flows for TDrive frontend

import { state } from '../state.js';
import {
    CheckSystemStatus, SaveSetup,
    LoginPhoneNumber, SumbitCode, SumbitPassword,
    CheckLoginStatus, InitDrive, MyUserID, SyncChannel,
} from '../../wailsjs/go/main/App';
import { renderBreadcrumb } from './navigation.js';
import { loadChannels } from './channels.js';
import { loadEncryptionStatus } from './encryption.js';
import { loadSelfUser } from './profile-menu.js';
import { notify, dismissNotification } from './notifications.js';

export function hideAllScreens() {
    const screens = ["setupcontainer", "phonecontainer", "codecontainer", "passwordcontainer", "success-screen"];
    screens.forEach(id => {
        const el = document.getElementById(id);
        if(el) el.style.display = "none";
    });
}

export function showAuthWrapper() {
    const authWrapper = document.getElementById("auth-wrapper");
    if (authWrapper) authWrapper.style.display = "flex";

    const dashboard = document.getElementById("success-screen");
    if (dashboard) dashboard.style.display = "none";
}

export function setupPasswordReveal() {
    const pw = document.getElementById("enterpassword");
    const toggle = document.getElementById("toggle-password");
    if (!pw || !toggle) return;

    const apply = (isVisible) => {
        pw.type = isVisible ? "text" : "password";

        toggle.dataset.state = isVisible ? "visible" : "hidden";
        toggle.setAttribute("aria-label", isVisible ? "Hide password" : "Show password");
        toggle.setAttribute("title", isVisible ? "Hide password" : "Show password");
    };

    apply(false);

    toggle.addEventListener("click", () => {
        const isVisible = pw.type === "password";
        apply(isVisible);
        pw.focus();
    });
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
            lastError = "Error: " + (err?.message || String(err));
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

    hideAllScreens();
    document.getElementById("success-screen").style.display = "flex";
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
        let status = await CheckSystemStatus();

        if (status === "NEEDS_SETUP") {
            showAuthWrapper();
            hideAllScreens();
            document.getElementById("setupcontainer").style.display = "block";
            return;
        }

        // Step B: Check Login
        let isLoggedIn = await CheckLoginStatus();
        if (isLoggedIn) {
            showDashboard();
        } else {
            showAuthWrapper();
            hideAllScreens();
            document.getElementById("phonecontainer").style.display = "block";
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

// Window bindings for auth functions
export function setupAuthWindowBindings() {
    window.submitSetup = function() {
        const id = parseInt(document.getElementById("api_id").value);
        const hash = document.getElementById("api_hash").value;
        if (!id || !hash) {
            notify({ level: 'warning', title: 'Enter both API ID and hash' });
            return;
        }

        SaveSetup(id, hash).then(res => {
            if (res === "Success") location.reload();
            else notify({ level: 'error', title: 'Setup failed', body: String(res) });
        });
    };

    window.startLogin = function () {
        const phone = (document.getElementById("enterphone").value || "").trim();
        if (!phone) {
            notify({ level: 'warning', title: 'Enter your phone number' });
            return;
        }

        state.lastLoginPhoneNumber = phone;

        LoginPhoneNumber(phone).then(() => {
            showAuthWrapper();
            hideAllScreens();
            document.getElementById("codecontainer").style.display = "block";

            const row = document.getElementById("code-target-row");
            const target = document.getElementById("code-target");
            if (target) target.innerText = state.lastLoginPhoneNumber;
            if (row) row.style.display = state.lastLoginPhoneNumber ? "flex" : "none";
        }).catch((err) => {
            notify({ level: 'error', title: 'Could not start login', body: String(err) });
        });
    };

    window.sendCode = function () {
        const code = document.getElementById("entercode").value;
        SumbitCode(code).catch((err) => {
            notify({ level: 'error', title: 'Could not submit code', body: String(err) });
        });
    };

    window.sendPassword = function () {
        SumbitPassword(document.getElementById("enterpassword").value).catch((err) => {
            notify({ level: 'error', title: 'Could not submit password', body: String(err) });
        });
    };

    window.runtime.EventsOn("login-success", () => showDashboard());

    window.runtime.EventsOn("login-password-required", () => {
        showAuthWrapper();
        hideAllScreens();
        document.getElementById("passwordcontainer").style.display = "block";

        const hintBox = document.getElementById("hint-box");
        const hintEl = document.getElementById("hinttext");
        if (hintEl) hintEl.innerText = "";
        if (hintBox) hintBox.style.display = "none";

        const pw = document.getElementById("enterpassword");
        const toggle = document.getElementById("toggle-password");
        if (pw) pw.type = "password";
        if (toggle) {
            toggle.dataset.state = "hidden";
            toggle.setAttribute("aria-label", "Show password");
            toggle.setAttribute("title", "Show password");
        }
    });

    window.runtime.EventsOn("login-error", (msg) => {
        notify({ level: 'error', title: 'Login failed', body: String(msg || 'Try again.') });
    });

    window.runtime.EventsOn("gothint", (hint) => {
        const hintEl = document.getElementById("hinttext");
        const hintBox = document.getElementById("hint-box");
        if (!hintEl || !hintBox) return;

        const text = (hint ?? "").toString().trim();
        const normalized = text.replace(/^(hint\s*:?[\s\u00A0]*)+/i, "").trim();

        if (!normalized || normalized.toLowerCase().includes("no hint")) {
            hintEl.innerText = "";
            hintBox.style.display = "none";
            return;
        }

        hintEl.innerText = normalized;
        hintBox.style.display = "block";
    });

    window.backToPhone = function () {
        showAuthWrapper();
        hideAllScreens();

        const phoneContainer = document.getElementById("phonecontainer");
        if (phoneContainer) phoneContainer.style.display = "block";

        const codeEl = document.getElementById("entercode");
        if (codeEl) codeEl.value = "";
    };
}
