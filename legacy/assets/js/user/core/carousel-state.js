// --- 3D CAROUSEL VARIABLES ---
let rotationY = 0;
let startX = 0;
let isDragging = false;
let autoRotate = true;
let carouselEl = null;

const authScreen = document.getElementById('auth-screen');
const appContainer = document.getElementById('app-container');
const loginForm = document.getElementById('login-form');
const signupForm = document.getElementById('signup-form');
const authError = document.getElementById('auth-error');
const initialLoader = document.getElementById('initial-loader');
