import { LoginPhoneNumber, SumbitCode, SumbitPassword } from '../wailsjs/go/main/App';

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
