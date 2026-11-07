// --- USER SPECIFIC DATA (Runs only after successful login) ---
const loadInitialData = () => {
    db.ref('movies').on('value', s => { 
        const rawMovies = s.val() || {};
        // শুধুমাত্র সেগুলোই ফিল্টার করছি যা প্রাইভেট নয়
        allMovies = {};
        Object.keys(rawMovies).forEach(id => {
            if (!rawMovies[id].isPrivate) {
                allMovies[id] = rawMovies[id];
            }
        });

        renderHomePage();
        renderCategoriesPage();

        if (!window.__startupRouteDone) {
            window.__startupRouteDone = true;
            const pending = sessionStorage.getItem('pending_movie');
            const lastPage = localStorage.getItem('last_page_id');
            const lastMovie = localStorage.getItem('last_movie_id');
            if (pending) {
                sessionStorage.removeItem('pending_movie');
                if (allMovies[pending]) showMovieDetails(pending, true);
                else if (typeof showCustomModal === 'function') showCustomModal('alert', 'This title is not available anymore.');
            } else if (lastPage === 'movie-details-page' && lastMovie && allMovies[lastMovie]) {
                showMovieDetails(lastMovie, false);
            }
        }
        if (typeof renderMyListPage === 'function') renderMyListPage();
    });

    db.ref('banners').on('value', s => renderBanners(s.val()));

    db.ref('socialLinks').on('value', s => renderSocials(s.val()));
    db.ref('notifications').on('value', s => renderNotifications(s.val()));
};
