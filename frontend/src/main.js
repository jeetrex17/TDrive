import {LoginPhoneNumber} from '../wailsjs/go/main/App';

window.startLogin = function () {
    
    let phone = document.getElementById("phone").value;

       LoginPhoneNumber(phone).then(() => {
        console.log("Go backend is now handling the login...");
    });
};
