// Global caches
let allMoviesCache = {};
let allCategoriesCache = {};
let allUsersCache = {};       // for user management
let onlineUsersCache = {};    // from online_users node
let allBrokenLinksCache = {}; // for broken links detection

const loginScreen = document.getElementById('login-screen');
const adminPanel = document.getElementById('admin-panel');
const adminPages = document.querySelectorAll('.admin-page');
const adminNavBtns = document.querySelectorAll('.admin-nav-btn');
const authErrorEl = document.getElementById('admin-auth-error');

// Filter states for users
let currentUserStatusFilter = 'all';
let currentUserSearchTerm = '';
