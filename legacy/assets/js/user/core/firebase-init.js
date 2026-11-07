if (!firebase.apps.length) firebase.initializeApp(firebaseConfig);
const auth = firebase.auth();
const db = mxCreateDb();   // MoveX server (SQLite) instead of Firebase Realtime Database
const analytics = firebase.analytics(); // এই লাইনটি নতুন যুক্ত করবেন
