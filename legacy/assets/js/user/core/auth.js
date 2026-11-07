// --- AUTH STATE & LOADING ---
auth.onAuthStateChanged(async (user) => {
    if (user) {
        // ১. ইউজারের রেকর্ড আপডেট করা এবং বর্তমান স্ট্যাটাস চেক করা
        const currentStatus = await ensureUserRecord(user);

        // ২. যদি একাউন্ট 'deleted' থাকে, তাহলে তার Auth Account (ইমেইল/পাসওয়ার্ড) পুরোপুরি মুছে দেওয়া হবে।
        if(currentStatus === "deleted") {
            showBannedPopup("deleted");
            try {
                // Firebase Auth থেকে একাউন্ট পার্মানেন্টলি ডিলিট করা
                await user.delete();
                // ডেটাবেজ থেকে রেকর্ড মুছে ফেলা
                db.ref('users/' + user.uid).remove();
            } catch(error) {
                // ফায়ারবেস রুলস অনুযায়ী অনেকদিন আগে লগইন করা থাকলে ডিলিট ফেইল হতে পারে, সেক্ষেত্রে জাস্ট লগআউট করে দিবো
                await auth.signOut();
            }
            return; 
        }

        // যদি একাউন্ট 'frozen' থাকে
        if(currentStatus === "frozen") {
            showBannedPopup("frozen");
            await auth.signOut();
            return; 
        }

        // ৩. রিয়েল-টাইম স্ট্যাটাস লিসেনার (লগইন থাকা অবস্থায় এডমিন ফ্রিজ/ডিলিট করলে সাথে সাথে বের করে দেবে)
        if (userStatusRef) userStatusRef.off(); 
        userStatusRef = db.ref('users/' + user.uid + '/status');

        userStatusRef.on('value', async (snap) => {
            const status = snap.val();

            if (status === "deleted") {
                showBannedPopup("deleted");
                try {
                    await auth.currentUser.delete();
                    db.ref('users/' + user.uid).remove();
                } catch(e) {
                    await auth.signOut();
                }
            } else if (status === "frozen") {
                showBannedPopup("frozen");
                await auth.signOut();
            }
        });

        // সবকিছু ঠিক থাকলে অ্যাপে প্রবেশ করানো
        authScreen.classList.remove('visible');
        authScreen.style.display = 'none'; 
        appContainer.classList.remove('hidden');
        updateUserProfile(user);
        setupPresence(user);

        const lastPage = localStorage.getItem('last_page_id');
        const lastMovie = localStorage.getItem('last_movie_id');

        if(lastPage && lastPage !== 'home-page') {
            if(lastPage !== 'movie-details-page') {
                showPage(lastPage, false);
            }
        } else {
            showPage('home-page', true);
        }

        initialLoader.style.opacity = '0';
        setTimeout(() => {
            initialLoader.style.display = 'none';
            (function run() { if (typeof loadInitialData === 'function') loadInitialData(); else setTimeout(run, 40); })();
        }, 500);


    } else {
        if (userStatusRef) {
            userStatusRef.off();
            userStatusRef = null;
        }
        authScreen.style.display = 'flex';
        setTimeout(() => authScreen.classList.add('visible'), 10);
        appContainer.classList.add('hidden');
        initialLoader.style.opacity = '0';
        setTimeout(() => initialLoader.style.display = 'none', 500);
    }
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
