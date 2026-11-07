// Public catalog data is read from Go API. Movie cards stay lightweight; full details load on demand.
(function () {
    'use strict';
    let refreshTimer = null;
    let loadingUID = null;
    let loadingPromise = null;
    let catalogRequestID = 0;

    function currentUID() {
        try {
            const user = firebase.auth().currentUser;
            return user && user.uid ? user.uid : null;
        } catch (_) { return null; }
    }
    function mapByID(items) {
        const out = {};
        (Array.isArray(items) ? items : []).forEach(item => { if (item && item.id) out[item.id] = item; });
        return out;
    }
    function refreshCatalog() {
        const uid = currentUID();
        if (!uid) return Promise.resolve();
        if (loadingPromise && loadingUID === uid) return loadingPromise;
        const requestID = ++catalogRequestID;
        loadingUID = uid;
        loadingPromise = MX_API.allMovies().then(items => {
            if (requestID !== catalogRequestID || currentUID() !== uid) return;
            allMovies = mapByID(items);
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
        }).catch(err => {
            if (requestID !== catalogRequestID || currentUID() !== uid) return;
            console.error('MoveX catalog load failed:', err);
            if (!Object.keys(allMovies || {}).length && typeof showCustomModal === 'function') showCustomModal('alert', 'Could not load titles. Check your connection and try again.');
        }).finally(() => {
            if (requestID === catalogRequestID) {
                loadingUID = null;
                loadingPromise = null;
            }
        });
        return loadingPromise;
    }
    function refreshSmallCatalogs() {
        return Promise.all([
            MX_API.banners().then(d => renderBanners(mapByID(d && d.items))).catch(() => {}),
            MX_API.socialLinks().then(d => renderSocials(mapByID(d && d.items))).catch(() => {}),
            MX_API.notifications({ limit: 100 }).then(d => renderNotifications(mapByID(d && d.items))).catch(() => {})
        ]);
    }
    window.loadInitialData = function () {
        refreshCatalog().then(() => {
            if (currentUID()) return refreshSmallCatalogs();
        });
        if (refreshTimer) clearInterval(refreshTimer);
        refreshTimer = setInterval(() => {
            if (document.visibilityState === 'visible' && currentUID()) {
                refreshCatalog().then(() => {
                    if (currentUID()) return refreshSmallCatalogs();
                });
            }
        }, 60000);
    };
    window.stopUserDataRefresh = function () {
        if (refreshTimer) clearInterval(refreshTimer);
        refreshTimer = null;
        catalogRequestID++;
        loadingUID = null;
        loadingPromise = null;
        allMovies = {};
        if (typeof renderHomePage === 'function') renderHomePage();
        if (typeof renderCategoriesPage === 'function') renderCategoriesPage();
        if (typeof renderMyListPage === 'function') renderMyListPage();
    };
    document.addEventListener('visibilitychange', () => {
        if (document.visibilityState === 'visible' && window.__mxDataStarted) {
            refreshCatalog().then(refreshSmallCatalogs);
        }
    });
    const original = window.loadInitialData;
    window.loadInitialData = function () { window.__mxDataStarted = true; return original(); };
})();
