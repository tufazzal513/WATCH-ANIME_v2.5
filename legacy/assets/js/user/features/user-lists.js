// --- MY LIST + CONTINUE WATCHING (stored in Firebase under the user's own account) ---
window.myListIds = {};       // { movieId: addedAt }
window.progressMap = {};     // { movieId: {ep, season, t, d, at} }
let _listUid = null, _listRef = null, _progRef = null, _statsRef = null;

auth.onAuthStateChanged(user => {
    if (_listRef) _listRef.off(); if (_progRef) _progRef.off(); if (_statsRef) _statsRef.off();
    myListIds = {}; progressMap = {}; _listUid = user ? user.uid : null;
    if (!user) return;
    _statsRef = db.ref('stats');
    _statsRef.on('value', st => { window.mxStats = st.val() || {}; if (typeof renderHomePage === 'function' && Object.keys(allMovies).length) renderHomePage(); }, () => {});
    _listRef = db.ref('users/' + user.uid + '/myList');
    _progRef = db.ref('users/' + user.uid + '/progress');
    _listRef.on('value', s => { myListIds = s.val() || {}; refreshListUI(); });
    _progRef.once('value', s => { progressMap = s.val() || {}; if (typeof renderHomePage === 'function' && Object.keys(allMovies).length) renderHomePage(); });   // read once; saveProgress keeps progressMap up to date locally
});

window.inMyList = (id) => !!myListIds[id];
window.toggleMyList = (id, ev) => {
    if (ev) ev.stopPropagation();
    if (!_listUid || !id) return;
    const ref = db.ref('users/' + _listUid + '/myList/' + id), adding = !myListIds[id];
    if (adding || (mxStats[id] && mxStats[id].lists > 0)) db.ref('stats/' + id + '/lists').transaction(n => Math.max(0, (n || 0) + (adding ? 1 : -1))).catch(() => {});
    (myListIds[id] ? ref.remove() : ref.set(firebase.database.ServerValue.TIMESTAMP)).catch(() => {
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
    renderMyListPage();
    if (typeof renderHomePage === 'function' && Object.keys(allMovies).length) renderHomePage();
};
window.renderMyListPage = () => {
    const grid = document.getElementById('mylist-grid'), empty = document.getElementById('mylist-empty');
    if (!grid) return;
    const mode = (document.getElementById('ml-sort') || {}).value || 'added';
    let items = Object.entries(myListIds).map(([id, at]) => [id, allMovies[id], at]).filter(([_, m]) => m);
    if (mode === 'az') items.sort((a, b) => String(a[1].title).localeCompare(String(b[1].title)));
    else if (mode === 'top') items.sort((a, b) => MX.ratingNum(b[1]) - MX.ratingNum(a[1]));
    else items.sort((a, b) => (b[2] || 0) - (a[2] || 0));
    empty.classList.toggle('hidden', items.length > 0);
    document.getElementById('ml-count').textContent = items.length ? items.length + (items.length === 1 ? ' title' : ' titles') : '';
    const pct = (id) => { const p = progressMap[id]; return p && p.d ? Math.min(100, Math.round(p.t / p.d * 100)) : 0; };
    grid.innerHTML = items.map(([id, m]) => {
        const genres = MX.genresOf(m).slice(0, 3).map(g => `<span class="ml-g">${esc(g)}</span>`).join('');
        return `
        <div class="ml-row" onclick="showMovieDetails('${id}')">
            <div class="ml-poster" ${mxT(m)}>${mxImg(m)}${mxBadges(m)}${pct(id) ? `<div class="nf-prog"><i style="width:${pct(id)}%"></i></div>` : ''}</div>
            <div class="ml-info">
                <p class="ml-title">${esc(m.title)}</p>
                ${mxMeta(m, false)}
                <div class="ml-genres">${genres}</div>
            </div>
            <div class="ml-act">
                <span class="ml-play"><i class="fas fa-play"></i></span>
                <button class="ml-rm" onclick="toggleMyList('${id}', event)" aria-label="Remove from My List"><i class="fas fa-bookmark"></i></button>
            </div>
        </div>`;
    }).join('');
};

// --- watch progress (resume) ---
let _lastSave = 0;
window.saveProgress = (movieId, season, ep, t, d, force) => {
    if (!_listUid || !movieId || !(d > 0) || !(t >= 0)) return;
    const now = Date.now();
    if (!force && now - _lastSave < 30000) return;   // was 8 s
    _lastSave = now;
    const ref = db.ref('users/' + _listUid + '/progress/' + movieId);
    const refreshHome = () => { if (force && typeof renderHomePage === 'function' && Object.keys(allMovies).length) renderHomePage(); };
    if (t / d > 0.95) { delete progressMap[movieId]; ref.remove(); refreshHome(); return; }       // finished: drop it
    progressMap[movieId] = { season: season || null, ep: ep == null ? null : ep, t: Math.floor(t), d: Math.floor(d), at: Date.now() };
    ref.set({ season: season || null, ep: ep == null ? null : ep, t: Math.floor(t), d: Math.floor(d), at: firebase.database.ServerValue.TIMESTAMP });
    refreshHome();
};

// count one "play" per user per title per 6 hours, only after 30 seconds of real watching (feeds the Hot lists)
window.mxCountPlay = (id) => {
    if (!id || !_listUid) return;
    const k = 'mx_played_' + id, last = +localStorage.getItem(k) || 0;
    if (Date.now() - last < 6 * 3600e3) return;
    localStorage.setItem(k, String(Date.now()));
    db.ref('stats/' + id + '/plays').transaction(n => (n || 0) + 1).catch(() => {});
};
