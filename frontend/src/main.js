
import { 
    LoginPhoneNumber, SumbitCode, SumbitPassword, 
    GetFileList, DownloadFile, DeleteFile, // <--- Checked this?
    CheckLoginStatus, InitDrive, SelectFile, UploadToTelegram 
} from '../wailsjs/go/main/App';


window.onload = async function() {
    console.log("App loaded.");
    try {
        let isLoggedIn = await CheckLoginStatus();
        if (isLoggedIn) {
            document.getElementById("phonecontainer").style.display = "none";
            document.getElementById("success-screen").style.display = "block";
            callInitDrive(); 
        } else {
            document.getElementById("phonecontainer").style.display = "block";
        }
    } catch (err) { console.error(err); }
};

window.startLogin = function () {
    let phone = document.getElementById("enterphone").value;
    LoginPhoneNumber(phone).then(() => {
        document.getElementById("phonecontainer").style.display = "none";
        document.getElementById("codecontainer").style.display = "block";
    });
};

window.sendCode = function () {
    let code = document.getElementById("entercode").value; 
    SumbitCode(code).then(() => {
        document.getElementById("codecontainer").style.display = "none";
        document.getElementById("passwordcontainer").style.display = "block";
    });
};

window.sendPassword = function () {
    let pass = document.getElementById("enterpassword").value;
    SumbitPassword(pass).then(() => {
        document.getElementById("passwordcontainer").style.display = "none";
        document.getElementById("result").innerText = "Login Complete";
    });
};

function callInitDrive() {
    InitDrive().then((result) => {
        document.getElementById("result").innerText = result;
    });
}

window.selectFile = function() {
    SelectFile().then((path) => {
        if (path === "") return;
        document.getElementById("result").innerText = "Uploading... ⏳";
        UploadToTelegram(path).then((msg) => {
            document.getElementById("result").innerText = msg;
            window.refreshFiles(); // Auto-refresh after upload
        });
    });
};


window.runtime.EventsOn("login-success", function() {
    console.log("EVENT RECEIVED: Login Success!");
    
   
    document.getElementById("phonecontainer").style.display = "none";
    document.getElementById("codecontainer").style.display = "none";
    document.getElementById("passwordcontainer").style.display = "none";

    
    document.getElementById("success-screen").style.display = "block";

        callInitDrive();
    window.refreshFiles();});

window.refreshFiles = function() {
    const listContainer = document.getElementById("file-list");
    listContainer.innerHTML = "Loading... 📡";

    GetFileList().then((files) => {
        if (!files || files.length === 0) {
            listContainer.innerHTML = "No files found.";
            return;
        }

        listContainer.innerHTML = "";
        files.forEach((file) => {
            const row = document.createElement("div");
            row.className = "file-row";
            const kbSize = (file.size / 1024).toFixed(1);

            row.innerHTML = `
                <div class="file-info">
                    <span class="file-name">${file.name}</span>
                    <span class="file-size">${kbSize} KB</span>
                </div>
                <div class="action-buttons">
                    <button class="download-btn" onclick="initDownload(${file.id})">⬇️</button>
                    <button class="delete-btn" onclick="initDelete(${file.id})">🗑️</button>
                </div>
            `;
            listContainer.appendChild(row);
        });
    });
};


window.initDownload = function(fileId) {
    console.log("Download clicked:", fileId);
    document.getElementById("result").innerText = "Downloading...";
    
    DownloadFile(fileId).then((result) => {
        alert(result);
        document.getElementById("result").innerText = result;
    });
};

window.initDelete = function(fileId) {
    console.log("Force deleting ID:", fileId);
    document.getElementById("result").innerText = "Deleting... ⏳";

  
    DeleteFile(fileId).then((result) => {
        console.log("Backend Response:", result);
        
                document.getElementById("result").innerText = result; 
        
                window.refreshFiles();
    }).catch(err => {
        console.error("Delete failed:", err);
        document.getElementById("result").innerText = "Error: " + err;
    });
};
