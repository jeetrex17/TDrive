
import { LoginPhoneNumber, SumbitCode, SumbitPassword, CheckLoginStatus, InitDrive , SelectFile , UploadToTelegram } from '../wailsjs/go/main/App';


window.onload = async function() {
    console.log("App loaded, checking session...");
    
    try {
        let isLoggedIn = await CheckLoginStatus();

        if (isLoggedIn) {
            console.log("Auto login successfull");
            document.getElementById("phonecontainer").style.display = "none";
            document.getElementById("success-screen").style.display = "block";

           callInitDrive(); 
        } else {
            console.log("No session found. Showing login.");
            document.getElementById("phonecontainer").style.display = "block";
        }
    } catch (err) {
        console.error("Error checking login:", err);
        document.getElementById("phonecontainer").style.display = "block";
    }
};

window.startLogin = function () {
    
    let phone = document.getElementById("enterphone").value;

       LoginPhoneNumber(phone).then(() => {
        document.getElementById("phonecontainer").style.display = "none";
        document.getElementById("codecontainer").style.display = "block";
        console.log("Go backend is now handling the login");
    });
};



window.sendCode = function () {
    let code = document.getElementById("entercode").value; 

    SumbitCode(code).then(() => {
        
        document.getElementById("codecontainer").style.display = "none";
        document.getElementById("passwordcontainer").style.display = "block";
        console.log("Code sent, waiting for password");
    });
};


window.runtime.EventsOn("gothint" , function(hint) {
    console.log("Hint Recived : " , hint);
let hintBox = document.getElementById("hinttext");
    if (hintBox) {
        hintBox.innerText = hint;
    }
});


window.sendPassword = function () {
    let pass = document.getElementById("enterpassword").value;

    SumbitPassword(pass).then(() => {
        
        document.getElementById("passwordcontainer").style.display = "none";
        document.getElementById("result").innerText = "Login process complete";

        
    });
};

function callInitDrive() {
    InitDrive().then((result) => {
        console.log("Backend Response:", result);
        document.getElementById("result").innerText = result;
    });
}

window.selectFile = function() {
    SelectFile().then((path) => {
        if (path === "") {
            console.log("User cancelled file selection");
            return; 
        }

        console.log("User picked:", path);
    });
};


window.selectFile = function() {
    SelectFile().then((path) => {
        if (path === "") {
            return;         }
        
        console.log("User file path :", path);
        
        document.getElementById("result").innerText = "Uploading... Please wait.";

        UploadToTelegram(path).then((msg) => {
            console.log("Upload Complete:", msg);
            document.getElementById("result").innerText = msg;
        });
    });
};
