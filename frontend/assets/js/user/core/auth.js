// --- AUTH STATE & LOADING ---
let userStatusTimer = null;
let authTransitionID = 0;
let statusCheckUID = null;

function isCurrentAuthUser(user, transitionID) {
    return !!(user && auth.currentUser && auth.currentUser.uid === user.uid &&
        (transitionID === undefined || transitionID === authTransitionID));
}

async function handleBlockedAccount(user, status, transitionID) {
    if (status !== 'deleted' && status !== 'frozen') return false;
    if (!isCurrentAuthUser(user, transitionID)) return true;
    showBannedPopup(status);
    if (status === 'deleted') {
        try {
            // The Go API allows this operation only for an account already marked deleted.
            await MX_API.request('DELETE', '/api/v1/me', undefined, { worker: false });
            if (isCurrentAuthUser(user, transitionID) && typeof user.delete === 'function') await user.delete();
        } catch (error) {
            // A recent-login requirement may prevent Firebase account deletion. Access is still revoked.
            console.warn('Deleted account cleanup requires re-authentication:', error);
        }
    }
    if (isCurrentAuthUser(user, transitionID)) await auth.signOut();
    return true;
}

async function checkCurrentAccountStatus(user) {
    if (!user || !isCurrentAuthUser(user) || statusCheckUID === user.uid) return;
    statusCheckUID = user.uid;
    const transitionID = authTransitionID;
    try {
        const me = await MX_API.me();
        if (!isCurrentAuthUser(user, transitionID)) return;
        await handleBlockedAccount(user, me && me.status, transitionID);
    } catch (error) {
        if (!isCurrentAuthUser(user, transitionID)) return;
        if (error && error.status === 403 && error.body && error.body.status) {
            await handleBlockedAccount(user, error.body.status, transitionID);
        } else if (error && error.status === 401) {
            await auth.signOut();
        }
    } finally {
        if (statusCheckUID === user.uid) statusCheckUID = null;
    }
}

auth.onAuthStateChanged(async (user) => {
    const transitionID = ++authTransitionID;
    if (userStatusTimer) { clearInterval(userStatusTimer); userStatusTimer = null; }
    if (window.__presenceCleanup) window.__presenceCleanup();
    if (!user) {
        if (typeof window.stopUserDataRefresh === 'function') window.stopUserDataRefresh();
        localStorage.removeItem('last_page_id');
        localStorage.removeItem('last_movie_id');
        authScreen.style.display = 'flex';
        setTimeout(() => {
            if (transitionID === authTransitionID && !auth.currentUser) authScreen.classList.add('visible');
        }, 10);
        appContainer.classList.add('hidden');
        initialLoader.style.opacity = '0';
        setTimeout(() => {
            if (transitionID === authTransitionID && !auth.currentUser) initialLoader.style.display = 'none';
        }, 500);
        return;
    }

    let currentStatus;
    try {
        currentStatus = await ensureUserRecord(user);
    } catch (error) {
        if (!isCurrentAuthUser(user, transitionID)) return;
        console.error('MoveX account initialization failed:', error);
        if (typeof showCustomModal === 'function') showCustomModal('alert', 'Could not verify your account. Please check your connection and sign in again.');
        await auth.signOut();
        return;
    }
    if (!isCurrentAuthUser(user, transitionID)) return;
    if (await handleBlockedAccount(user, currentStatus, transitionID)) return;
    if (!isCurrentAuthUser(user, transitionID)) return;

    authScreen.classList.remove('visible');
    authScreen.style.display = 'none';
    appContainer.classList.remove('hidden');
    updateUserProfile(user);
    setupPresence(user);

    const lastPage = localStorage.getItem('last_page_id');
    if (lastPage && lastPage !== 'home-page' && lastPage !== 'movie-details-page') showPage(lastPage, false);
    else showPage('home-page', true);

    initialLoader.style.opacity = '0';
    setTimeout(() => {
        if (!isCurrentAuthUser(user, transitionID)) return;
        initialLoader.style.display = 'none';
        (function run() {
            if (!isCurrentAuthUser(user, transitionID)) return;
            if (typeof loadInitialData === 'function') loadInitialData();
            else setTimeout(run, 40);
        })();
    }, 500);
    userStatusTimer = setInterval(() => {
        if (transitionID === authTransitionID && document.visibilityState === 'visible') checkCurrentAccountStatus(user);
    }, 60000);
});

function updateUserProfile(user) {
    const headerUserName = document.getElementById('header-user-name');
    if(headerUserName) headerUserName.textContent = `Hi, ${user.displayName ? user.displayName.split(' ')[0] : 'User'}`;
    const appName = localStorage.getItem('movex_app_name') || "MoveX";
    document.getElementById('profile-name').textContent = user.displayName || `${appName} User`;
    document.getElementById('profile-email').textContent = user.email;
    document.getElementById('profile-initial').textContent = user.displayName ? user.displayName.charAt(0).toUpperCase() : 'U';
}

const pages = document.querySelectorAll('.page');
const navBtns = document.querySelectorAll('.nav-btn');

// Function to handle when Global Top Header should be visible or hidden
function toggleGlobalHeader(show) {
    const globalHeader = document.getElementById('global-header');
    const mainContent = document.querySelector('.main-content');
    if (show) {
        if (globalHeader) globalHeader.style.display = 'flex';
        if (mainContent) mainContent.classList.remove('no-global-header');
    } else {
        if (globalHeader) globalHeader.style.display = 'none';
        if (mainContent) mainContent.classList.add('no-global-header');
    }
}

window.onpopstate = (event) => {
    // leave fullscreen/stop the inline player first when the phone Back button is used
    if (typeof stopPlayer === 'function') stopPlayer();
    if (event.state && event.state.page === 'movie-details-page' && event.state.movieId) {
        showMovieDetails(event.state.movieId, false);
    } else if (event.state && event.state.page) {
        showPage(event.state.page, false);
    } else {
        showPage('home-page', false);
    }
};

const showPage = (pageId, pushToHistory = true) => {
    if (typeof closeSheet === 'function') closeSheet();
    pages.forEach(page => page.classList.toggle('active', page.id === pageId));
    navBtns.forEach(btn => {
        btn.classList.toggle('text-blue-500', btn.dataset.page === pageId);
        btn.classList.toggle('text-gray-400', btn.dataset.page !== pageId);
    });

    // Handle Global App Bar visibility based on active page
    const hideHeaderPages = ['movie-details-page', 'category-movies-page', 'search-page'];
    toggleGlobalHeader(!hideHeaderPages.includes(pageId));

    localStorage.setItem('last_page_id', pageId);
    if (pushToHistory) history.pushState({ page: pageId }, null, "");

    if (pageId === 'search-page' && typeof resetSearchPage === 'function') resetSearchPage();
};

window.goBack = () => history.back();
navBtns.forEach(btn => btn.addEventListener('click', () => showPage(btn.dataset.page)));
