
import { 
    CheckSystemStatus, SaveSetup,
    LoginPhoneNumber, SumbitCode, SumbitPassword, 
    GetFileList, DownloadFile, DeleteFile, 
    CheckLoginStatus, InitDrive, SelectFile, UploadToTelegram 
} from '../wailsjs/go/main/App';

let pendingDeleteID = null;

function formatDate(unixTimestamp) {
    if (!unixTimestamp) return "-";
    const date = new Date(unixTimestamp * 1000);
    return date.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' });
}

function formatBytes(bytes) {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
}

// NO EMOJIS HERE. ONLY TEXT BADGES.
function getFileBadge(filename) {
    if (!filename || typeof filename !== "string" || !filename.includes(".")) {
        return `<span class="file-badge badge-ext">FILE</span>`;
    }

    const rawExt = filename.split(".").pop();
    const ext = (rawExt || "")
        .replace(/[^a-z0-9]/gi, "")
        .toUpperCase()
        .slice(0, 4);

    return `<span class="file-badge badge-ext">${ext || "FILE"}</span>`;
}

window.onload = async function() {
    console.log("App loaded. Checking Status...");
    setupDeleteModal();

    try {
        // Step A: Check Setup
        // If this fails, it's because Wails bindings are missing. Restart Wails!
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
            // Ensure login screen is visible if not logged in
            showAuthWrapper();
            hideAllScreens();
            document.getElementById("phonecontainer").style.display = "block";
        }

    } catch (err) {
        console.error("Startup Crash:", err);
        // Don't hide everything if we crash. Let the user see the console error.
        alert("Startup Error: " + err + "\n\nDid you restart 'wails dev'?");
    }
};

function hideAllScreens() {
    const screens = ["setupcontainer", "phonecontainer", "codecontainer", "passwordcontainer", "success-screen"];
    screens.forEach(id => {
        const el = document.getElementById(id);
        if(el) el.style.display = "none";
    });
}

function showAuthWrapper() {
    const authWrapper = document.getElementById("auth-wrapper");
    if (authWrapper) authWrapper.style.display = "flex";

    const dashboard = document.getElementById("success-screen");
    if (dashboard) dashboard.style.display = "none";
}

function setupDeleteModal() {
    const modal = document.getElementById("delete-modal");
    const cancelBtn = document.getElementById("delete-cancel");
    const confirmBtn = document.getElementById("delete-confirm");

    if (!modal || !cancelBtn || !confirmBtn) return;

    const close = () => {
        pendingDeleteID = null;
        modal.style.display = "none";
    };

    cancelBtn.addEventListener("click", close);
    modal.addEventListener("click", (e) => {
        if (e.target === modal) close();
    });

    confirmBtn.addEventListener("click", () => {
        const id = pendingDeleteID;
        close();
        if (typeof id !== "number") return;

        const status = document.getElementById("status-msg");
        if (status) status.innerText = "Deleting...";

        DeleteFile(id).then((res) => {
            if (status) status.innerText = res || "Done";
            window.refreshFiles();
            setTimeout(() => {
                if (status) status.innerText = "Ready";
            }, 2000);
        });
    });
}

window.submitSetup = function() {
    const id = parseInt(document.getElementById("api_id").value);
    const hash = document.getElementById("api_hash").value;
    if (!id || !hash) return alert("Enter both fields.");

    SaveSetup(id, hash).then(res => {
        if(res === "Success") location.reload();
        else alert(res);
    });
};

window.startLogin = function () {
    const phone = document.getElementById("enterphone").value;
    if(!phone) return alert("Enter phone number");
    
    LoginPhoneNumber(phone).then(() => {
        showAuthWrapper();
        hideAllScreens();
        document.getElementById("codecontainer").style.display = "block";
    });
};

window.sendCode = function () {
    const code = document.getElementById("entercode").value;
    SumbitCode(code).then(() => {
        showAuthWrapper();
        hideAllScreens();
        document.getElementById("passwordcontainer").style.display = "block";
    });
};

window.sendPassword = function () {
    SumbitPassword(document.getElementById("enterpassword").value);
};

window.runtime.EventsOn("login-success", () => showDashboard());

function showDashboard() {
    const authWrapper = document.getElementById("auth-wrapper");
    if (authWrapper) authWrapper.style.display = "none";

    hideAllScreens();
    document.getElementById("success-screen").style.display = "flex";
    InitDrive().then(() => window.refreshFiles());
}

window.refreshFiles = function() {
    const list = document.getElementById("file-list");
    const storageUsed = document.getElementById("storage-used");

    list.innerHTML = '<div style="padding:20px; color:#565f89;">Loading...</div>';
    if (storageUsed) storageUsed.innerText = "Calculating... / Unlimited";

    GetFileList().then((files) => {
        const allFiles = Array.isArray(files) ? files : [];

        if (storageUsed) {
            const totalBytes = allFiles.reduce((sum, file) => sum + (file?.size || 0), 0);
            storageUsed.innerText = `${formatBytes(totalBytes)} / Unlimited`;
        }

        if (allFiles.length === 0) {
            list.innerHTML = '<div style="padding:20px; color:#565f89;">No files found.</div>';
            return;
        }

        list.innerHTML = "";
        allFiles.forEach((file) => {
            const row = document.createElement("div");
            row.className = "file-row";
            
            // CLEAN SVG ICONS
            const downloadIcon = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4"/></svg>`;
            const trashIcon = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"/></svg>`;

            row.innerHTML = `
                <div class="row-name">
                    ${getFileBadge(file.name)}
                    ${file.name}
                </div>
                <div class="row-meta">${formatDate(file.date)}</div>
                <div class="row-meta">${formatBytes(file.size)}</div>
                <div class="row-actions">
                    <button class="action-icon download" type="button" title="Download">${downloadIcon}</button>
                    <button class="action-icon del delete" type="button" title="Delete">${trashIcon}</button>
                </div>
            `;
            const downloadBtn = row.querySelector("button.download");
            if (downloadBtn) {
                downloadBtn.addEventListener("click", () => window.initDownload(file.id));
            }
            const deleteBtn = row.querySelector("button.delete");
            if (deleteBtn) {
                deleteBtn.addEventListener("click", () => window.initDelete(file.id));
            }
            list.appendChild(row);
        });
    });
};

window.selectFile = function() {
    SelectFile().then(path => {
        if (!path) return;
        document.getElementById("status-msg").innerText = "Uploading...";
        UploadToTelegram(path).then(() => {
            document.getElementById("status-msg").innerText = "Ready";
            window.refreshFiles();
        });
    });
};

window.initDownload = function(id) {
    document.getElementById("status-msg").innerText = "Downloading...";
    DownloadFile(id).then(res => {
        alert(res);
        document.getElementById("status-msg").innerText = "Ready";
    });
};

window.initDelete = function(id) {
    pendingDeleteID = id;
    const modal = document.getElementById("delete-modal");
    if (modal) modal.style.display = "flex";
};
