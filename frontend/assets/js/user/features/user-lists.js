// My List and continue-watching data are private Go API resources.
window.myListIds = {};
window.progressMap = {};
let _listUid = null;
let _lastSave = 0;
let _listRefreshTimer = null;

function refreshMyListAndProgress() {
    const requestUID = _listUid;
    if (!requestUID || !auth.currentUser || auth.currentUser.uid !== requestUID) return Promise.resolve();
    return Promise.all([MX_API.myList(), MX_API.progress()]).then(([listData, progressData]) => {
        if (_listUid !== requestUID || !auth.currentUser || auth.currentUser.uid !== requestUID) return;
        myListIds = {};
        (listData && listData.items || []).forEach(item => { myListIds[item.movieId] = item.addedAt; });
        progressMap = {};
        (progressData && progressData.items || []).forEach(item => {
            progressMap[item.movieId] = { season: item.season || null, ep: item.ep == null ? null : item.ep, t: item.t, d: item.d, at: item.at };
        });
        refreshListUI();
        if (typeof renderHomePage === 'function' && Object.keys(allMovies || {}).length) renderHomePage();
    }).catch(err => { console.error('MoveX user data load failed:', err); });
}

auth.onAuthStateChanged(user => {
    _listUid = user ? user.uid : null;
    myListIds = {}; progressMap = {};
    if (_listRefreshTimer) clearInterval(_listRefreshTimer);
    _listRefreshTimer = null;
    if (!user) { refreshListUI(); return; }
    refreshMyListAndProgress();
    _listRefreshTimer = setInterval(() => {
        if (document.visibilityState === 'visible' && auth.currentUser) refreshMyListAndProgress();
    }, 60000);
});

window.inMyList = id => !!myListIds[id];
window.toggleMyList = (id, ev) => {
    if (ev) ev.stopPropagation();
    if (!_listUid || !id) return;
    const adding = !myListIds[id];
    const action = adding ? MX_API.addToList(id) : MX_API.removeFromList(id);
    action.then(() => refreshMyListAndProgress()).catch(() => {
        if (typeof showCustomModal === 'function') showCustomModal('alert', 'Could not update My List. Please try again.');
    });
};

window.refreshListUI = () => {
    document.querySelectorAll('[data-mylist]').forEach(b => {
        const on = inMyList(b.dataset.mylist);
        b.classList.toggle('on', on);
        const i = b.querySelector('i'); if (i) i.className = on ? 'fas fa-check' : 'fas fa-plus';
        const t = b.querySelector('.lbl'); if (t) t.textContent = on ? 'In My List' : 'Add to list';
    });
    if (typeof renderMyListPage === 'function') renderMyListPage();
    if (typeof renderHomePage === 'function' && Object.keys(allMovies || {}).length) renderHomePage();
};

window.renderMyListPage = () => {
    const grid = document.getElementById('mylist-grid'), empty = document.getElementById('mylist-empty');
    if (!grid) return;
    const mode = (document.getElementById('ml-sort') || {}).value || 'added';
    let items = Object.entries(myListIds).map(([id, at]) => [id, allMovies[id], at]).filter(([_, m]) => m);
    if (mode === 'az') items.sort((a, b) => String(a[1].title).localeCompare(String(b[1].title)));
    else if (mode === 'top') items.sort((a, b) => MX.ratingNum(b[1]) - MX.ratingNum(a[1]));
    else items.sort((a, b) => (b[2] || 0) - (a[2] || 0));
    if (empty) empty.classList.toggle('hidden', items.length > 0);
    const count = document.getElementById('ml-count');
    if (count) count.textContent = items.length ? items.length + (items.length === 1 ? ' title' : ' titles') : '';
    const pct = id => { const p = progressMap[id]; return p && p.d ? Math.min(100, Math.round(p.t / p.d * 100)) : 0; };
    grid.innerHTML = items.map(([id, m]) => {
        const genres = MX.genresOf(m).slice(0, 3).map(g => `<span class="ml-g">${esc(g)}</span>`).join('');
        return `<div class="ml-row" onclick="showMovieDetails('${esc(id)}')"><div class="ml-poster" ${mxT(m)}>${mxImg(m)}${mxBadges(m)}${pct(id) ? `<div class="nf-prog"><i style="width:${pct(id)}%"></i></div>` : ''}</div><div class="ml-info"><p class="ml-title">${esc(m.title)}</p>${mxMeta(m, false)}<div class="ml-genres">${genres}</div></div><div class="ml-act"><span class="ml-play"><i class="fas fa-play"></i></span><button class="ml-rm" onclick="toggleMyList('${esc(id)}', event)" aria-label="Remove from My List"><i class="fas fa-bookmark"></i></button></div></div>`;
    }).join('');
};

window.saveProgress = (movieId, season, ep, t, d, force) => {
    if (!_listUid || !movieId || !(d > 0) || !(t >= 0)) return;
    const now = Date.now();
    if (!force && now - _lastSave < 30000) return;
    _lastSave = now;
    if (t / d > 0.95) {
        delete progressMap[movieId];
        MX_API.deleteProgress(movieId).catch(() => {});
        if (force && typeof renderHomePage === 'function') renderHomePage();
        return;
    }
    const data = { season: season || '', ep: ep == null ? null : ep, t: Math.floor(t), d: Math.floor(d) };
    progressMap[movieId] = Object.assign({}, data, { at: now });
    MX_API.setProgress(movieId, data).catch(err => console.error('MoveX progress save failed:', err));
    if (force && typeof renderHomePage === 'function') renderHomePage();
};

window.mxCountPlay = id => {
    if (!id || !_listUid) return;
    const key = 'mx_played_' + id, last = +localStorage.getItem(key) || 0;
    if (Date.now() - last < 6 * 3600e3) return;
    localStorage.setItem(key, String(Date.now()));
    MX_API.viewMovie(id).catch(() => {});
};
