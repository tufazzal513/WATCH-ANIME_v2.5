// Firebase is used for Google sign-in only. MoveX application data lives in the Go API.
// Firebase Realtime Database and Firebase Analytics SDKs are intentionally not loaded.
if (!window.MX || !window.MX.firebase) {
  throw new Error('MoveX runtime config must load before firebase-config.js');
}
const firebaseConfig = window.MX.firebase;
