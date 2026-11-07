if (!firebase.apps.length) firebase.initializeApp(firebaseConfig);
const auth = firebase.auth();
// Firebase is used only for Authentication. All application data uses the Go API.
