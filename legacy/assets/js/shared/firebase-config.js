// Firebase project config (shared by the user and admin panels)
const firebaseConfig = {
  apiKey: "AIzaSyBKBffOSZJgJcKastQEL71X7MlJlFhiPOo",
  authDomain: "movex-513.firebaseapp.com",
  databaseURL: "https://movex-513-default-rtdb.asia-southeast1.firebasedatabase.app",
  projectId: "movex-513",
  storageBucket: "movex-513.firebasestorage.app",
  messagingSenderId: "850803322721",
  appId: "1:850803322721:web:e4f7d47a2612647d0f5f20"
};

// MoveX database server (the Node + SQLite app in /server).  Firebase is now used for Auth only.
// Leave "" if the site and the server share one domain; otherwise e.g. "https://your-app.botkeep.cloud"
const MX_API_BASE = "";
