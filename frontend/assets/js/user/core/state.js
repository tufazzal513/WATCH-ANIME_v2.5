// --- Global Variables ---
let allMovies = {};
let allCategories = {};
let currentMovieId = null;
let adTimer = 15;
let adUrl = "https://monetag.com"; 
let pwdResetLimit = 3; // default
let forgetCooldown = 0; // Cooldown timer for reset
