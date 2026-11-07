
firebase.initializeApp(firebaseConfig);
const auth = firebase.auth();
const db = mxCreateDb();   // MoveX server (SQLite) instead of Firebase Realtime Database
